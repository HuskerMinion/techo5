package alarm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/api"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/musicassistant"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// Waking with music (#651). An alarm can wake somebody with a radio station or a Music Assistant
// favorite in place of its alarm sound. It is still a ring in every way but the sound: it shows, it is
// stopped and snoozed and rings out as any other, and Home Assistant's events are the same. The music
// plays on this device's own speaker, the way the radio page or the music library plays it here, at
// the ring volume rather than the music's for as long as it rings (so a volume turned down the night
// before does not make it quiet), and comes up from quiet as the alarm sound does when it gets louder.
//
// The music has to be heard within wakeWithin, or the alarm sound rings instead, so that a station that
// is down, a network that is not there or a Music Assistant that does not answer never means somebody
// oversleeps; music that stops while the alarm rings has as long to come back. Music that cannot be
// played at all - no Music Assistant set up, a station gone from the radio's list - rings the alarm
// sound at once.
//
// A snooze pauses a Music Assistant favorite, and the snooze ringing resumes it where it was; a resume
// that does not come within half of wakeWithin starts the favorite again. A radio stream is live, with
// nowhere to resume from: a snooze stops it and the snooze ringing starts it again. A stop stops it.

var (
	// wakeWithin is how long the music has to be heard before the alarm sound takes over: long enough
	// for a station to connect and Music Assistant to start, short enough that an alarm that cannot
	// play is soon audibly an alarm.
	wakeWithin = 8 * time.Second

	// wakeEvery is how often the music's level is set while it rings: often enough that it comes up
	// smoothly, and goes down at once when the ring is silenced or hushed for a word.
	wakeEvery = 250 * time.Millisecond

	// startQuiet is the bell's ring with no sound of its own, and goWake what plays the music for it;
	// variables for the tests.
	startQuiet = ring.StartQuiet
	goWake     = func(f func()) { safe.Go("alarm music", f) }

	// wakeReady says why music cannot be played now, or nil when it can be tried.
	wakeReady = musicReady
	// wakeStart starts the music; it may wait, up to wakeWithin, until it plays or did not.
	wakeStart = startMusic
	// wakeResume plays Music Assistant's queue here again from where it was paused.
	wakeResume = resumeMusic
	// wakeHeard is whether music is what the room hears.
	wakeHeard = func() bool { return media.Get().Audible() }
	// wakeStop stops the music, and wakePause pauses it.
	wakeStop  = stopMusic
	wakePause = func() { media.Get().PauseAudible() }
	// wakeLower holds the music db under full, 0 for full (speaker.Arbiter.Lower).
	wakeLower = func(db int) { speaker.Sound().Backgrounds().Lower(db) }
	// wakeVolume plays the music at step, the ring volume, until release (media.Player.HoldVolume).
	wakeVolume = func(step int) (release func()) { return media.Get().HoldVolume(step) }
)

// wakeHushed is the music while the ring is silenced or hushed for a word: as far down as a duck goes.
const wakeHushed = -60

// wakeOf is what the device's alarm key (or its snooze) wakes with; the alarm sound for a helper.
func wakeOf(key string) config.WakeWith {
	id := strings.TrimPrefix(key, "snooze:")
	for _, al := range config.Get().Alarms.List {
		if al.ID == id {
			return al.Waking()
		}
	}
	return config.WakeWith{}
}

// wakeTry is one way of getting the music going: resuming it, or starting it.
type wakeTry struct {
	how    string
	within time.Duration
	start  func(context.Context) error
}

// playWake plays w for the ring r, which StartQuiet started, for as long as r rings unsnoozed; resume
// is the ring of a snooze that paused it. sound gives the ring its alarm sound when the music cannot be
// played or is not heard in time.
func (a *Alarms) playWake(r *Ring, w config.WakeWith, rise time.Duration, resume bool, sound func([]speaker.Note, time.Duration)) {
	still := func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.ringing == r && !r.snoozed
	}
	release := func() {}
	tone := func(why string, err error) {
		slog.Warn("alarm: ringing the alarm sound instead of the music", "wake_with", w.String(), "why", why, "err", err)
		a.mu.Lock()
		r.playing = false
		a.mu.Unlock()
		wakeLower(0)
		release()
		if still() {
			sound(speaker.AlarmSound(a.Sound()), rise)
		}
	}
	if err := wakeReady(w); err != nil {
		tone("it cannot be played now", err)
		return
	}
	// Music already playing would be taken for the alarm's.
	if wakeHeard() {
		wakeStop()
	}
	release = wakeVolume(ring.Level())
	defer func() { release() }()
	floor := int(math.Round(ring.RiseDB(0, rise)))
	wakeLower(floor)
	defer wakeLower(0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tries := []wakeTry{{"start", wakeWithin, func(ctx context.Context) error { return wakeStart(ctx, w, still) }}}
	if resume && w.Kind == config.WakeMusic {
		tries = append([]wakeTry{{"resume", wakeWithin / 2, wakeResume}}, tries...)
	}
	var (
		try     wakeTry
		started chan error
		asked   time.Time
	)
	// next begins the next try, and says whether there was one.
	next := func() bool {
		if len(tries) == 0 {
			return false
		}
		try, tries = tries[0], tries[1:]
		ch := make(chan error, 1)
		started, asked = ch, time.Now()
		start := try.start
		tctx, tcancel := context.WithTimeout(ctx, try.within+time.Second)
		safe.Go("alarm music", func() { defer tcancel(); ch <- start(tctx) })
		return true
	}
	next()

	// heard is when the music was first heard, and last when it was last: music that stops while the
	// alarm rings (a station that drops and does not come back) is given wakeWithin too.
	var heard, last time.Time
	for still() {
		now := time.Now()
		on := wakeHeard()
		if heard.IsZero() && !on {
			var err error
			select {
			case err = <-started:
			default:
			}
			if err != nil || now.Sub(asked) >= try.within {
				slog.Info("alarm: the music did not come", "how", try.how, "wake_with", w.String(), "err", err)
				wakeStop()
				if !next() {
					tone("it was not heard in time", err)
					return
				}
			}
		}
		switch {
		case on:
			if heard.IsZero() {
				heard = now
				a.mu.Lock()
				r.playing = true
				a.mu.Unlock()
				slog.Info("alarm: waking with music", "wake_with", w.String(), "how", try.how, "after", now.Sub(asked).Round(time.Millisecond))
			}
			last = now
		case !heard.IsZero() && now.Sub(last) >= wakeWithin:
			wakeStop()
			tone("it stopped and did not come back", nil)
			return
		}
		db := floor
		if !heard.IsZero() {
			db = int(math.Round(ring.RiseDB(now.Sub(heard), rise)))
		}
		if ring.Quiet() {
			db = wakeHushed
		}
		wakeLower(db)
		time.Sleep(wakeEvery)
	}
	a.mu.Lock()
	snoozed := r.snoozed
	a.mu.Unlock()
	if snoozed && w.Kind == config.WakeMusic && !heard.IsZero() {
		wakePause()
		return
	}
	wakeStop()
}

// musicReady is wakeReady: why w cannot be played now, or nil.
func musicReady(w config.WakeWith) error {
	// The music plays at the ring volume, so a ring volume of 0 is as silent for it as for the alarm
	// sound: somebody chose that, and the alarm sound is what they chose it for.
	if ring.Level() == 0 {
		return errors.New("the ring volume is 0")
	}
	switch w.Kind {
	case config.WakeRadio:
		// A station the device plays itself is played from its address. One Home Assistant plays has to
		// be on the radio's list still, and Home Assistant there to play it.
		if w.URI != "" {
			return nil
		}
		if !slices.Contains(home.Get().Radio().Stations, w.Name) {
			return errors.New("the station is not on the radio's list now")
		}
		if !haConnected() {
			return errors.New("this device is not connected to Home Assistant")
		}
		return nil
	case config.WakeMusic:
		if !config.Get().MusicAssistant.Set() {
			return errors.New("no music library is set up on this device")
		}
		if w.URI == "" {
			return errors.New("no favorite to play")
		}
		return nil
	}
	return errors.New("nothing to play")
}

// haConnected is whether Home Assistant is connected to this device; a variable for the tests.
var haConnected = func() bool { return api.Get().Connected() }

// startMusic is wakeStart: it plays w here as the radio page or the music library would. wanted says
// whether it is still wanted, for a start that takes a while.
func startMusic(ctx context.Context, w config.WakeWith, wanted func() bool) error {
	switch w.Kind {
	case config.WakeRadio:
		if w.URI != "" {
			if !home.Get().PlayStream(w.Name, w.URI) {
				return fmt.Errorf("%s has no stream address this device can play", w.Name)
			}
			return nil
		}
		home.Get().Play(w.Name)
		return nil
	case config.WakeMusic:
		c, player, err := maPlayer()
		if err != nil {
			return err
		}
		return c.PlayChecked(ctx, player, w.URI, wakeWithin, time.Second, wanted)
	}
	return errors.New("nothing to play")
}

// resumeMusic is wakeResume: this device's player at Music Assistant plays its queue again from where
// it was paused (the way a play on the device resumes it, home's resumeMusicAssistant).
func resumeMusic(ctx context.Context) error {
	c, own, err := maPlayer()
	if err != nil {
		return err
	}
	p, err := c.Player(ctx, own)
	if err != nil {
		return err
	}
	if !p.Available {
		return errors.New("this device is not connected to the music library right now")
	}
	return c.Resume(ctx, p.ID)
}

// stopMusic is wakeStop: what plays here stops, as the radio page's Stop stops it.
func stopMusic() {
	if home.Following() {
		// The page is another room's player, and its Stop leaves the music here alone.
		media.Get().Transport(media.TransportStop)
		media.Get().Stop()
		return
	}
	home.Get().Stop()
}

// maPlayer is the Music Assistant set up on this device and this device's player there.
func maPlayer() (musicassistant.Client, string, error) {
	m := config.Get().MusicAssistant
	if !m.Set() {
		return musicassistant.Client{}, "", errors.New("no music library is set up on this device")
	}
	player, err := layout.FactoryMAC()
	if err != nil || player == "" {
		return musicassistant.Client{}, "", errors.New("this device does not know its own player id")
	}
	return musicassistant.Client{URL: m.URL, Token: m.Token}, player, nil
}

// The music an alarm can wake with.

// wakeFavoritesFor is how long Music Assistant's favorites are kept before they are asked for again.
const wakeFavoritesFor = 10 * time.Minute

// wakeNoLibraryAgain is how soon a device with no Music Assistant set up looks again.
const wakeNoLibraryAgain = 5 * time.Second

var wakeFavorites struct {
	sync.Mutex
	list []config.WakeWith
	at   time.Time
	// fetching is closed when the fetch under way ends; nil for none.
	fetching chan struct{}
}

var (
	// wakeStations is the radio's stations, as its page lists them; a variable for the tests.
	wakeStations = radioStations
	// fetchFavorites asks Music Assistant for its favorites; a variable for the tests.
	fetchFavorites = maFavorites
)

// WakeChoices is the music an alarm can wake with, as the editors offer it: the radio's stations, then
// Music Assistant's favorite playlists, albums and radio stations. Music Assistant's are kept for a
// while, and asked for again once they are older; wait is how long to wait for that, 0 for not at all,
// and Changed fires when they arrive.
func (a *Alarms) WakeChoices(wait time.Duration) []config.WakeWith {
	out := wakeStations()
	wakeFavorites.Lock()
	if time.Since(wakeFavorites.at) > wakeFavoritesFor && wakeFavorites.fetching == nil {
		done := make(chan struct{})
		wakeFavorites.fetching = done
		safe.Go("alarm music choices", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			list, err := fetchFavorites(ctx)
			wakeFavorites.Lock()
			if errors.Is(err, errNoLibrary) {
				// Nothing to ask, and nothing kept as an answer either: a library set up a minute later
				// is asked within seconds rather than ten minutes on. Not at once, though: the Wake with
				// list asks on every frame it is drawn. Changed only fires when favorites go, since an
				// editor drawn again for it would ask again, and fire it again.
				had := len(wakeFavorites.list) > 0
				wakeFavorites.list, wakeFavorites.fetching = nil, nil
				wakeFavorites.at = time.Now().Add(-wakeFavoritesFor + wakeNoLibraryAgain)
				wakeFavorites.Unlock()
				close(done)
				if had {
					a.Changed.Emit(struct{}{})
				}
				return
			}
			if err == nil {
				wakeFavorites.list, wakeFavorites.at = list, time.Now()
			} else {
				slog.Info("alarm music choices: Music Assistant's favorites were not read", "err", err)
				wakeFavorites.at = time.Now().Add(-wakeFavoritesFor + time.Minute) // asked again in a minute
			}
			wakeFavorites.fetching = nil
			wakeFavorites.Unlock()
			close(done)
			a.Changed.Emit(struct{}{})
		})
	}
	fetching := wakeFavorites.fetching
	wakeFavorites.Unlock()
	if fetching != nil && wait > 0 {
		select {
		case <-fetching:
		case <-time.After(wait):
		}
	}
	wakeFavorites.Lock()
	out = append(out, wakeFavorites.list...)
	wakeFavorites.Unlock()
	return out
}

// radioStations is wakeStations: the stations the radio page lists, with the address of each the
// device plays itself, so the alarm plays it without the list (which a Radio Browser list may not
// have loaded yet at six in the morning).
func radioStations() []config.WakeWith {
	var out []config.WakeWith
	for _, name := range home.Get().Radio().Stations {
		w := config.WakeWith{Kind: config.WakeRadio, Name: name}
		if url, ok := home.Get().StreamOf(name); ok {
			w.URI = url
		}
		out = append(out, w)
	}
	return out
}

// errNoLibrary is fetchFavorites with no Music Assistant set up to ask.
var errNoLibrary = errors.New("no music library is set up")

// maFavorites is fetchFavorites: Music Assistant's favorite playlists, albums and radio stations.
func maFavorites(ctx context.Context) ([]config.WakeWith, error) {
	m := config.Get().MusicAssistant
	if !m.Set() {
		return nil, errNoLibrary
	}
	c := musicassistant.Client{URL: m.URL, Token: m.Token}
	var out []config.WakeWith
	var firstErr error
	for _, kind := range []string{musicassistant.KindPlaylists, musicassistant.KindAlbums, musicassistant.KindRadios} {
		got, err := c.Favorites(ctx, kind, 40)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, e := range got {
			if e.Name != "" && e.URI != "" {
				out = append(out, config.WakeWith{Kind: config.WakeMusic, Name: e.Name, URI: e.URI})
			}
		}
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// wakeParseWait is how long ParseWake waits for Music Assistant's favorites when the name is not
// among those it has.
const wakeParseWait = 3 * time.Second

// ParseWake reads what an alarm wakes with as Home Assistant writes it (config.WakeWith's String):
// "alarm_sound", "radio: <station>" or "music_assistant: <favorite>", the name as WakeChoices lists it.
// A Music Assistant URI, or a stream's address for the radio, is taken as it is.
func (a *Alarms) ParseWake(s string) (config.WakeWith, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "alarm_sound") || strings.EqualFold(s, "alarm sound") {
		return config.WakeWith{}, nil
	}
	kind, name, _ := strings.Cut(s, ":")
	kind, name = strings.ToLower(strings.TrimSpace(kind)), strings.TrimSpace(name)
	switch kind {
	case config.WakeRadio:
	case config.WakeMusic, "music assistant":
		kind = config.WakeMusic
	default:
		return config.WakeWith{}, fmt.Errorf(`alarms: %.64q is not something to wake with; give "alarm_sound", "radio: <station>" or "music_assistant: <favorite>"`, s)
	}
	if name == "" {
		return config.WakeWith{}, fmt.Errorf("alarms: %.64q names no %s", s, kind)
	}
	// The list as it is first, and only then waiting for Music Assistant's favorites, and not for long:
	// this runs on the Home Assistant connection's read loop, which hears nothing else while it waits.
	// The wait is only for a fetch under way, a new favorite or the first after a start.
	find := func(wait time.Duration) (config.WakeWith, bool) {
		for _, c := range a.WakeChoices(wait) {
			if c.Kind == kind && (strings.EqualFold(c.Name, name) || c.URI == name) {
				return c, true
			}
		}
		return config.WakeWith{}, false
	}
	if c, ok := find(0); ok {
		return c, nil
	}
	if c, ok := find(wakeParseWait); ok {
		return c, nil
	}
	if strings.Contains(name, "://") && (kind == config.WakeMusic || strings.HasPrefix(name, "http")) {
		return config.WakeWith{Kind: kind, Name: name, URI: name}, nil
	}
	return config.WakeWith{}, fmt.Errorf("alarms: no %s called %.64q on this device", strings.ReplaceAll(kind, "_", " "), name)
}

// SetWake sets what the device's alarm id wakes with, from Home Assistant (alarm_wake_with).
func (a *Alarms) SetWake(id string, w config.WakeWith) error {
	id = strings.TrimSpace(id)
	for _, al := range config.Get().Alarms.List {
		if al.ID != id {
			continue
		}
		if w.Music() && (al.Remind || al.Silent || al.LightOnly) {
			return errors.New("alarms: only an alarm that rings wakes with music, not a reminder, a silent alarm or a light-only one")
		}
		al.Wake = w
		if err := a.Put(al); err != nil {
			return err
		}
		slog.Info("alarm wake with set from home assistant", "id", id, "wake_with", w.String())
		return nil
	}
	return fmt.Errorf("alarms: no alarm %.64q on this device", id)
}
