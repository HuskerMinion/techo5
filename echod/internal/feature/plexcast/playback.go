//go:build !dot

package plexcast

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// What is playing, and what the device is doing with it. One item at a time, as a player has: a second
// playMedia replaces the first, and what a controller asks afterwards -- pause, seek, the volume, the
// next in the queue -- is about this one.
//
// The device's own players do the playing: a song is a received track (feature/media), so a voice turn
// ducks it and an alarm sounds over it, and a film is feature/video's full-screen player. This is
// where the two are told apart and where their state is read back, so a pause on the screen, a pause
// from the phone and a pause because a call came in all look the same to Plex.

// The states a Plex timeline names.
const (
	statePlaying   = "playing"
	statePaused    = "paused"
	stateStopped   = "stopped"
	stateBuffering = "buffering"
	stateError     = "error"
)

// errNothingPlaying is a transport command with nothing to act on. Plex's players answer it as a
// failed command rather than pretending it worked.
var errNothingPlaying = errors.New("nothing is playing")

// reportEvery is how often the Plex server is told what the device is doing while it plays, as Plex's
// own API asks: on every change, and every ten seconds after.
const reportEvery = 10 * time.Second

type playback struct {
	f *Feature

	mu      sync.Mutex
	gen     int // which start is current: one overtaken by another leaves everything alone
	req     mediaRequest
	it      *item
	isVideo bool
	state   string
	session string // one per playback, not per command: the server's dashboard keys on it

	videoID uint64

	// Where it is: started and base are the moment and offset it began at, and pausedFor is the time
	// spent paused since. The position is those three, added up, which is also what a seek restarts.
	started   time.Time
	base      time.Duration
	pausedFor time.Duration
	pausedAt  time.Time

	audio, subtitle int
	// subtitles is the device's own answer for the film on screen, nil while it is the file's: burned
	// captions are part of the picture the server makes, so turning them on or off is a new transcode.
	subtitles       *bool
	shuffle, repeat int
	queueID, itemID string

	cancel   context.CancelFunc
	reported time.Time
}

func newPlayback(f *Feature) *playback { return &playback{f: f, state: stateStopped} }

// start plays an item, in place of whatever was playing. The previous one is stopped first -- its
// stream closed, its film stopped, its transcode told to stop on the server -- so two never share the
// speaker or a core of the server.
func (p *playback) start(req mediaRequest, it *item) error {
	p.mu.Lock()
	p.gen++
	gen := p.gen
	oldCancel, oldVideo, oldReq, oldSession := p.cancel, p.videoID, p.req, p.session
	p.req, p.it = req, it
	p.session = uuid.NewString()
	session := p.session
	p.isVideo = it.video
	p.state = stateBuffering
	p.started, p.base, p.pausedFor, p.pausedAt = time.Now(), req.offset, 0, time.Time{}
	p.audio, p.subtitle = it.part.audioStream(), it.part.subtitleStream()
	p.subtitles = nil
	p.queueID, p.itemID = queueIDs(req.container)
	p.reported = time.Time{}
	p.cancel, p.videoID = nil, 0
	p.mu.Unlock()

	if oldCancel != nil {
		oldCancel()
	}
	if oldVideo != 0 {
		video.StopID(oldVideo)
	}
	if oldReq.address != "" && oldSession != "" {
		safe.Go("plex stop stream", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			stopTranscode(ctx, oldReq, oldSession)
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.mu.Lock()
	if p.gen != gen {
		p.mu.Unlock()
		cancel()
		return nil
	}
	p.cancel = cancel
	p.mu.Unlock()

	if it.video {
		return p.startVideo(ctx, gen, req, it, session)
	}
	safe.Go("plex audio", func() { p.startAudio(ctx, gen, req, it, session) })
	return nil
}

// startVideo is a film: the server's decided stream, full screen, with its sound.
func (p *playback) startVideo(ctx context.Context, gen int, req mediaRequest, it *item, session string) error {
	subs := p.subtitleOn()
	u, err := p.videoURL(ctx, req, it, subs, session)
	if err != nil {
		p.failed(gen, err)
		return err
	}
	// Where to begin is the player's: the stream is the whole title, and a resume is a seek into it.
	id, err := video.Play(video.Request{URL: u, Title: it.title, Origin: video.FromPlex, StartAt: p.at()})
	if err != nil {
		p.failed(gen, err)
		return err
	}
	p.mu.Lock()
	if p.gen != gen {
		p.mu.Unlock()
		video.StopID(id)
		return nil
	}
	p.videoID, p.state = id, statePlaying
	p.mu.Unlock()
	p.changed()
	return nil
}

// startAudio is a song: fetched, decoded and handed to the speaker as a received track, exactly as
// DLNA's renderer does it.
func (p *playback) startAudio(ctx context.Context, gen int, req mediaRequest, it *item, session string) {
	u := p.audioURL(req, it, p.at(), session)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		p.failed(gen, err)
		return
	}
	hreq.Header.Set("X-Plex-Client-Identifier", clientIdentifier())
	hreq.Header.Set("X-Plex-Token", req.token)
	resp, err := fetchClient.Do(hreq)
	if err == nil && resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		err = fmt.Errorf("the server answered %s", resp.Status)
	}
	var pcm io.Reader
	if err == nil {
		pcm, err = media.DecodeStream(bufio.NewReaderSize(resp.Body, 64<<10), resp.Header.Get("Content-Type"))
		if err != nil {
			resp.Body.Close()
		}
	}
	if err != nil {
		p.failed(gen, err)
		return
	}
	p.mu.Lock()
	if p.gen != gen {
		p.mu.Unlock()
		resp.Body.Close()
		return
	}
	title, artist, album := it.title, it.artist, it.album
	pos := p.atLocked(time.Now())
	p.state = statePlaying
	p.mu.Unlock()

	home.ReceivedPicture(home.PlexName, nil)
	src := newSource(pcm, resp.Body, func() { p.ended(gen) })
	media.Get().PlayReceived(home.PlexName, src, speaker.Rate, 2)
	media.Get().SetReceivedTrack(home.PlexName, title, artist, album)
	media.SetPosition(media.Position{Title: title, At: time.Now(), Pos: pos, Dur: it.dur, Rate: 1})
	safe.Go("plex cover", func() { p.cover(gen, req, it) })
	p.changed()
}

// cover fetches the item's picture for Now Playing, unless something else has started since.
func (p *playback) cover(gen int, req mediaRequest, it *item) {
	if it.thumb == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		req.server()+it.thumb+"?X-Plex-Token="+url.QueryEscape(req.token), nil)
	if err != nil {
		return
	}
	resp, err := fetchClient.Do(hreq)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return
	}
	p.mu.Lock()
	current := p.gen == gen
	p.mu.Unlock()
	if current {
		home.ReceivedPicture(home.PlexName, b)
	}
}

// subtitleOn is whether the film should have its subtitles burned in: what the device last said, else a
// subtitle the casting app chose, else one the file has selected.
func (p *playback) subtitleOn() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.subtitleOnLocked()
}

// subtitleOnLocked is subtitleOn with the lock already held. Wants p.mu.
func (p *playback) subtitleOnLocked() bool {
	if p.subtitles != nil {
		return *p.subtitles
	}
	return p.subtitle != 0 || (p.it != nil && p.it.part.subtitleStream() != 0)
}

// at is how far into the item the device is now.
func (p *playback) at() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.atLocked(time.Now())
}

// atLocked is at, with the lock already held. Wants p.mu.
func (p *playback) atLocked(now time.Time) time.Duration {
	d := p.base
	switch {
	case p.state == statePlaying:
		d += now.Sub(p.started) - p.pausedFor
	case p.state == statePaused && !p.pausedAt.IsZero():
		d += p.pausedAt.Sub(p.started) - p.pausedFor
	}
	if p.it != nil && p.it.dur > 0 && d > p.it.dur {
		d = p.it.dur
	}
	if d < 0 {
		d = 0
	}
	return d
}

// sync brings the state in line with the device's own players, which anybody can change: the screen's
// pause button, a call, an alarm, a station on the voice assistant. A song that is no longer the
// speaker's is over; one paused on the screen is paused here.
func (p *playback) sync() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != statePlaying && p.state != statePaused {
		return
	}
	if p.it == nil {
		return
	}
	if p.isVideo {
		vs := video.Current()
		switch vs.Phase {
		case video.Playing, video.Loading:
			p.resumeLocked()
		case video.Paused:
			p.pauseLocked()
		default:
			p.finishLocked(nil)
		}
		return
	}
	if media.Get().Receiving() != home.PlexName {
		p.finishLocked(nil)
		return
	}
	if _, paused := media.Get().ScreenState(); paused {
		p.pauseLocked()
	} else {
		p.resumeLocked()
	}
}

// pauseLocked starts the paused clock, once. Wants p.mu.
func (p *playback) pauseLocked() {
	if p.state == statePaused {
		return
	}
	p.state, p.pausedAt = statePaused, time.Now()
}

// resumeLocked adds the pause to the time spent paused and goes on. Wants p.mu.
func (p *playback) resumeLocked() {
	if p.state != statePaused {
		if p.state == stateBuffering {
			p.state = statePlaying
		}
		return
	}
	if !p.pausedAt.IsZero() {
		p.pausedFor += time.Since(p.pausedAt)
	}
	p.state, p.pausedAt = statePlaying, time.Time{}
}

// finishLocked ends what is playing, for an error where there is one. Wants p.mu.
func (p *playback) finishLocked(err error) {
	p.state = stateStopped
	if err != nil {
		p.state = stateError
	}
	p.pausedAt = time.Time{}
}

// ended is the song's stream running out: it played to its end by itself, which is the one end a
// controller is told about as the next item's business.
func (p *playback) ended(gen int) {
	p.mu.Lock()
	if p.gen != gen || p.state == stateStopped || p.state == stateError {
		p.mu.Unlock()
		return
	}
	p.finishLocked(nil)
	p.mu.Unlock()
	p.changed()
}

// failed is a start that could not finish: the controller is told the item failed, in the timeline's
// own words.
func (p *playback) failed(gen int, err error) {
	p.mu.Lock()
	if p.gen != gen {
		p.mu.Unlock()
		return
	}
	p.finishLocked(err)
	p.mu.Unlock()
	slog.Warn("plex: playback failed", "err", err)
	p.changed()
}

// changed is a state change every controller should hear at once: the timeline is pushed and the
// server is told, rather than waiting for the next second's clock.
func (p *playback) changed() {
	p.f.tl.push()
	safe.Go("plex report", func() { p.report() })
}

// control carries out a transport command.
type transportCmd int

const (
	transportPlay transportCmd = iota
	transportPause
	transportStop
)

func (f *Feature) transport(cmd transportCmd) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := readCommand(w, r); !ok {
			return
		}
		if err := f.pb.control(cmd); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		agree(w)
	}
}

func (p *playback) control(cmd transportCmd) error {
	p.mu.Lock()
	switch cmd {
	case transportPlay:
		if p.it == nil {
			p.mu.Unlock()
			return errNothingPlaying
		}
		if p.state == statePaused {
			paused := p.isVideo
			p.mu.Unlock()
			if paused {
				video.Resume()
			} else {
				media.Get().Resume()
			}
			p.mu.Lock()
			p.resumeLocked()
			p.mu.Unlock()
			p.changed()
			return nil
		}
		if p.state == stateStopped || p.state == stateError {
			// Where it stopped, not where the item began: a stop keeps its place.
			req, it := p.req, p.it
			req.offset = p.base
			p.mu.Unlock()
			return p.start(req, it)
		}
		p.mu.Unlock()
		return nil
	case transportPause:
		if p.state != statePlaying && p.state != stateBuffering {
			p.mu.Unlock()
			return errNothingPlaying
		}
		isVideo := p.isVideo
		p.pauseLocked()
		p.mu.Unlock()
		if isVideo {
			video.Pause()
		} else {
			media.Get().Pause()
			media.ClearPosition()
		}
		p.changed()
		return nil
	case transportStop:
		p.stopLocked()
		p.mu.Unlock()
		p.changed()
		return nil
	}
	p.mu.Unlock()
	return nil
}

// stopLocked ends what is playing and leaves the item set, as Plex's stop does: the queue and the
// position stay, so a play again picks up where it was. Wants p.mu.
func (p *playback) stopLocked() {
	// Where it is now is where it stays: a stop is not a rewind, and a play after it goes on from
	// there, which is what Plex's stop means to the app that sent it.
	pos := p.atLocked(time.Now())
	p.base, p.started, p.pausedFor, p.pausedAt = pos, time.Now(), 0, time.Time{}
	p.finishLocked(nil)
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	isVideo, id := p.isVideo, p.videoID
	p.videoID = 0
	if p.it != nil && !isVideo && media.Get().Receiving() == home.PlexName {
		media.ClearPosition()
		media.Get().Stop()
	}
	if isVideo && id != 0 {
		defer video.StopID(id)
	}
}

// stop is stopLocked with the lock taken, for a caller outside this file.
func (p *playback) stop() {
	p.mu.Lock()
	p.stopLocked()
	p.mu.Unlock()
	p.changed()
}

// off is the player going away with its account: nothing is left playing or remembered.
func (p *playback) off() {
	p.mu.Lock()
	p.stopLocked()
	p.gen++
	p.req, p.it, p.session = mediaRequest{}, nil, ""
	p.state = stateStopped
	p.mu.Unlock()
}

// seek moves what is playing to pos. A film moves inside its own stream, which the player does without
// asking the server anything, so a scrub from the phone costs a second rather than a new transcode per
// command -- a burst of them used to leave the film stopped. A song has no stream to move within, so
// its transcode is asked for again at the new second.
func (p *playback) seek(pos time.Duration) error {
	p.mu.Lock()
	req, it := p.req, p.it
	isVideo, videoID, state := p.isVideo, p.videoID, p.state
	p.mu.Unlock()
	if it == nil {
		return errNothingPlaying
	}
	if pos < 0 {
		pos = 0
	}
	if it.dur > 0 && pos > it.dur {
		pos = it.dur
	}
	if isVideo && videoID != 0 && state != stateBuffering {
		if id, ok := video.Seek(pos); ok {
			p.mu.Lock()
			p.videoID = id
			p.base, p.started, p.pausedFor = pos, time.Now(), 0
			if p.state == statePaused {
				p.pausedAt = time.Now()
			} else {
				p.pausedAt = time.Time{}
			}
			p.mu.Unlock()
			p.changed()
			return nil
		}
	}
	req.offset = pos
	return p.start(req, it)
}

// seekTo is the handler: an absolute offset in milliseconds.
func (f *Feature) seekTo(w http.ResponseWriter, r *http.Request) {
	q, ok := readCommand(w, r)
	if !ok {
		return
	}
	if err := f.pb.seek(q.millis("offset")); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	agree(w)
}

// step is a jump forward or back from where it is: 30 seconds on, as Plex's own players do, and 15
// back.
type stepDir int

const (
	forward stepDir = 1
	back    stepDir = -1
)

func (f *Feature) step(dir stepDir) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := readCommand(w, r); !ok {
			return
		}
		d := 30 * time.Second
		if dir == back {
			d = -15 * time.Second
		}
		if err := f.pb.seek(f.pb.at() + d); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		agree(w)
	}
}

// skip is the next or previous item in the play queue the controller sent with the item.
func (f *Feature) skip(delta int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := readCommand(w, r); !ok {
			return
		}
		if err := f.pb.skip(delta); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		agree(w)
	}
}

func (p *playback) skip(delta int) error {
	p.mu.Lock()
	req, it := p.req, p.it
	p.mu.Unlock()
	if it == nil || it.queue == "" {
		return errNothingPlaying
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	next, err := p.next(ctx, req, it, delta)
	if err != nil {
		return err
	}
	nreq := req
	nreq.key, nreq.offset = next.key, 0
	return p.start(nreq, next)
}

// queueIDs is the play queue's number and the item's, from the containerKey a controller sends
// (/playQueues/12?window=100&own=1). The timeline reports them, so the casting app shows the queue it
// made.
func queueIDs(container string) (queue, item string) {
	path, query, _ := strings.Cut(container, "?")
	if i := strings.LastIndex(path, "/"); i >= 0 {
		queue = path[i+1:]
	}
	for _, kv := range strings.Split(query, "&") {
		k, v, _ := strings.Cut(kv, "=")
		if k == "playQueueItemID" {
			item = v
		}
	}
	return queue, item
}

// source is a song's bytes on their way to the speaker: the decoder's output, the server's connection,
// and a word when the song runs out by itself. It is a media.PCMSource, as DLNA's renderer's is, and it
// keeps that one's rule: a deadline is for the read that asked for it and no longer, because the
// speaker stops reading while it is paused or a reply is spoken and a pause of any length must not end
// the song.
type source struct {
	r     io.Reader
	body  io.Closer
	ended func()
	once  sync.Once

	mu    sync.Mutex
	timer *time.Timer
}

func newSource(r io.Reader, body io.Closer, ended func()) *source {
	return &source{r: r, body: body, ended: ended}
}

func (s *source) Read(b []byte) (int, error) {
	n, err := s.r.Read(b)
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.mu.Unlock()
	if errors.Is(err, io.EOF) {
		s.once.Do(s.ended)
	}
	return n, err
}

func (s *source) Close() error {
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
	}
	s.mu.Unlock()
	return s.body.Close()
}

func (s *source) SetReadDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if !t.IsZero() {
		s.timer = time.AfterFunc(time.Until(t), func() { _ = s.body.Close() })
	}
	return nil
}

// volume is the device's volume as Plex says it: a percentage of the steps the speaker has.
func volume() int {
	return media.Get().Volume() * 100 / media.VolumeSteps
}

// volumeSteps is Plex's 0-100 as the device's steps.
func volumeSteps(pct int) int {
	pct = min(max(pct, 0), 100)
	return int(float64(pct) * float64(media.VolumeSteps) / 100)
}

// atoi is a parameter that must be a number, with Plex's own fallback.
func atoi(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
