//go:build !dot

package plexcast

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// What the casting app is told about the device: one MediaContainer of three Timelines, always all
// three even when two of them are idle, because a controller draws its buttons from them and reads the
// volume from the music one before anything plays.
//
// Two ways it reaches a controller, and Plex's own players offer both: the controller polls (Plex Web
// does, every half minute, holding each request with wait=1 until something changes), and a controller
// that subscribed is pushed to on every change and every second while something plays.

// pollHold is how long a wait=1 poll is held: under the thirty seconds controllers re-poll at, so a
// quiet player still answers before its controller gives up.
const pollHold = 25 * time.Second

// subFor is how long a subscription lasts without being renewed. Plex's own number is 90 seconds; a
// controller renews every 30.
const subFor = 90 * time.Second

type timeline struct {
	f *Feature

	mu   sync.Mutex
	subs map[string]*subscription
	poke chan struct{}
}

// subscription is one controller being pushed to: where, how long it lasts, and the last commandID it
// sent, which every timeline back to it carries so it can tell a stale command from a fresh one.
type subscription struct {
	url     string
	command string
	until   time.Time
}

func newTimeline(f *Feature) *timeline {
	return &timeline{f: f, subs: map[string]*subscription{}, poke: make(chan struct{}, 1)}
}

// clear forgets every subscriber, for an account going away.
func (t *timeline) clear() {
	t.mu.Lock()
	t.subs = map[string]*subscription{}
	t.mu.Unlock()
}

// body is the timeline's XML: the same text a push posts and a poll answers.
func (t *timeline) body(location, command string) string {
	s := t.f.pb.snapshot()
	var b strings.Builder
	b.WriteString(`<MediaContainer`)
	if location != "" {
		b.WriteString(` location="` + escape(location) + `"`)
	}
	if command != "" {
		b.WriteString(` commandID="` + escape(command) + `"`)
	}
	b.WriteString(`>`)
	for _, typ := range []string{"video", "music", "photo"} {
		b.WriteString(s.timeline(typ))
	}
	b.WriteString(`</MediaContainer>`)
	return b.String()
}

// snapshot is what the device is playing, taken under the player's lock so a timeline is one moment
// rather than a mix of two.
type snapshot struct {
	kind            string
	state           string
	pos, dur        time.Duration
	req             mediaRequest
	it              *item
	audio, subtitle int
	shuffle, repeat int
	queue, item     string
}

func (p *playback) snapshot() snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := snapshot{
		state: p.state, pos: p.atLocked(time.Now()),
		audio: p.audio, subtitle: p.subtitle, shuffle: p.shuffle, repeat: p.repeat,
		req: p.req, it: p.it, queue: p.queueID, item: p.itemID,
	}
	if p.it != nil {
		s.dur = p.it.dur
		s.kind = "music"
		if p.isVideo {
			s.kind = "video"
		}
	}
	return s
}

// timeline is one media type's entry. An idle type still says it is stopped -- and the music one says
// the volume, which is the device's, so the app's slider works before anything plays.
func (s snapshot) timeline(typ string) string {
	if s.it == nil || s.kind != typ {
		if typ == "music" {
			return `<Timeline type="music" state="stopped" volume="` + strconv.Itoa(volume()) +
				`" controllable="playPause,stop,volume"/>`
		}
		return `<Timeline type="` + typ + `" state="stopped"/>`
	}
	var b strings.Builder
	attr := func(name, value string) {
		if value == "" {
			return
		}
		b.WriteString(` ` + name + `="` + escape(value) + `"`)
	}
	b.WriteString(`<Timeline type="` + typ + `"`)
	attr("state", s.state)
	attr("time", strconv.FormatInt(s.pos.Milliseconds(), 10))
	attr("duration", strconv.FormatInt(s.dur.Milliseconds(), 10))
	attr("ratingKey", s.it.ratingKey)
	attr("key", s.it.key)
	attr("containerKey", s.it.queue)
	attr("machineIdentifier", s.req.machine)
	attr("address", s.req.address)
	attr("port", s.req.port)
	attr("protocol", s.req.protocol)
	attr("token", s.req.token)
	attr("volume", strconv.Itoa(volume()))
	attr("shuffle", strconv.Itoa(s.shuffle))
	attr("repeat", strconv.Itoa(s.repeat))
	attr("playQueueID", s.queue)
	attr("playQueueItemID", s.item)
	// Both spellings: Plex's spec writes ID, its own python client reads Id, and controllers do both.
	if s.audio != 0 {
		attr("audioStreamID", strconv.Itoa(s.audio))
		attr("audioStreamId", strconv.Itoa(s.audio))
	}
	if s.subtitle != 0 {
		attr("subtitleStreamID", strconv.Itoa(s.subtitle))
		attr("subtitleStreamId", strconv.Itoa(s.subtitle))
	}
	attr("controllable", "playPause,stop,volume,seekTo,stepBack,stepForward,skipPrevious,skipNext")
	b.WriteString(`/>`)
	return b.String()
}

// push sends the timeline to every subscriber whose subscription is still good. It is called on every
// change and once a second while something plays, so it does the least it can: a copy of the list under
// the lock, then requests without it.
func (t *timeline) push() {
	// A poll held on wait=1 is let go at once: a change is what it was waiting for.
	select {
	case t.poke <- struct{}{}:
	default:
	}
	now := time.Now()
	t.mu.Lock()
	var out []*subscription
	for id, s := range t.subs {
		if now.After(s.until) {
			delete(t.subs, id)
			continue
		}
		out = append(out, s)
	}
	t.mu.Unlock()
	if len(out) == 0 {
		return
	}
	body := t.body("fullScreenVideo", "")
	for _, s := range out {
		go post(context.Background(), s.url, body)
	}
}

// post pushes a timeline to one controller, with the header that identifies the player sending it.
func post(ctx context.Context, url, body string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("X-Plex-Client-Identifier", clientIdentifier())
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	resp.Body.Close()
}

// poll answers a controller that asks rather than subscribes. wait=1 holds the request until the
// timeline changes or pollHold passes; wait=0 answers at once.
func (f *Feature) poll(w http.ResponseWriter, r *http.Request) {
	q, ok := readCommand(w, r)
	if !ok {
		return
	}
	command := q.get("commandID")
	if q.get("wait") == "1" {
		select {
		case <-f.tl.poke:
		case <-time.After(pollHold):
		case <-r.Context().Done():
			return
		}
	}
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, f.tl.body("", command))
}

// subscribe registers a controller to be pushed to, at the address it came from and the port it names.
func (f *Feature) subscribe(w http.ResponseWriter, r *http.Request) {
	q, ok := readCommand(w, r)
	if !ok {
		return
	}
	id := r.Header.Get("X-Plex-Client-Identifier")
	if id == "" || id == clientIdentifier() {
		// A subscription with no controller named is nothing that can be pushed to, and one naming this
		// player is this player hearing itself.
		fail(w, http.StatusBadRequest, "no controller")
		return
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	protocol := q.get("protocol")
	if protocol != "https" {
		protocol = "http"
	}
	port := q.number("port", 32500)
	f.tl.subscribe(id, protocol+"://"+net.JoinHostPort(host, strconv.Itoa(port))+"/:/timeline", q.get("commandID"))
	agree(w)
}

// subscribe records one controller's push address, and starts its first timeline at once.
func (t *timeline) subscribe(id, url, command string) {
	t.mu.Lock()
	t.subs[id] = &subscription{url: url, command: command, until: time.Now().Add(subFor)}
	t.mu.Unlock()
	safe.Go("plex push", func() { post(context.Background(), url, t.body("fullScreenVideo", command)) })
	select {
	case t.poke <- struct{}{}:
	default:
	}
}

// unsubscribe drops a controller that is going away, or that polled instead.
func (f *Feature) unsubscribe(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("X-Plex-Client-Identifier")
	f.tl.mu.Lock()
	delete(f.tl.subs, id)
	f.tl.mu.Unlock()
	agree(w)
}
