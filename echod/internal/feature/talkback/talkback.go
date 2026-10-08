// Package talkback sends the device's microphones to a camera's own speaker: Talk on the camera page,
// for answering the door from the kitchen. It goes straight to the camera, over the backchannel of its
// RTSP stream (ONVIF Profile T, lib/onvifback), so it needs no Home Assistant, go2rtc or Frigate in
// between: a camera on a Reolink recorder set up here is found by itself, any other is given its
// RTSP address on the setup page (config.TalkBack).
//
// The microphones go after echo cancellation, halved to 8 kHz and in G.711, which is what cameras
// with a speaker take. The camera's own sound, where it has one playing, can keep playing on the
// device meanwhile: the echo canceller takes it out of what is sent back.
//
// Talk is a toggle, not press-to-talk: a conversation at the door is not something to hold a finger
// on the glass through. The room is only sent while the camera page, with its red Talk bar, is what is
// on the screen: a talk ends on a second tap, the view closing, turning to another camera or being
// covered by anything else (a call, a ring, the settings), the Talk through cameras switch going off,
// the microphones being muted, the camera hanging up, or two minutes passing. While it lasts the view
// does not time out, and the wake word and the action button do not start a turn, as in a call.
package talkback

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/halfrate"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/onvifback"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

const (
	// maxTalk is the longest a talk runs: long enough for any conversation at a door, and a limit on
	// a microphone left sending to the yard by mistake.
	maxTalk = 2 * time.Minute

	// openWait bounds the setting up. A camera behind a hub can take eight seconds to offer its
	// backchannel (lib/onvifback asks up to three times).
	openWait = 20 * time.Second

	// holdView is how far ahead the view is kept up while a talk runs, renewed every check.
	holdView = 15 * time.Second

	// failShown is how long a talk that could not start or broke off says why on the camera page.
	failShown = 6 * time.Second

	// maxError is the most of a failure's words the camera page shows: they can come from the camera.
	maxError = 100

	// talkLevel is what the microphones are turned down by for a talk, 12 dB under what recognition
	// is leveled to. A doorbell's small speaker overdrives at that level and its own microphone takes
	// it back in, so the visitor hears a loud, looping voice (#60).
	talkLevel = 0.25
)

// check is how often a running talk looks at what ends it besides the camera, on top of looking
// whenever one of those things says it changed. A variable so that a test need not wait a second.
var check = time.Second

// seenWithin is how recently the screen must have drawn the camera page with its Talk bar for a talk
// to go on: the Show draws four times a second and the Spot eight, so anything drawn over the page for
// longer than this has covered it. A variable for the tests, which have no screen.
var seenWithin = 2 * time.Second

// Phase is where a talk is.
type Phase int

const (
	Idle    Phase = iota
	Opening       // asking the camera for its backchannel
	Talking
)

// State is what the camera page shows for its Talk control.
type State struct {
	Entity string // the camera being talked to, or the one whose talk failed
	Phase  Phase
	Left   time.Duration // of the two minutes, while talking
	Error  string        // why the last talk to Entity failed, for a few seconds after
}

type Feature struct {
	// op makes Start, Stop and Toggle one at a time: two taps close together must not both start a
	// talk, which would leave one sending that nothing could stop.
	op sync.Mutex

	mu      sync.Mutex
	state   State
	gen     int       // which talk the state belongs to; a talk that is not the latest leaves it alone
	until   time.Time // the two minutes' end
	failAt  time.Time
	seen    string // the camera the screen last drew with Talk, and when
	seenAt  time.Time
	cancel  context.CancelFunc
	done    chan struct{} // closed when the running talk's goroutine is gone
	poke    chan struct{} // something that can end a talk changed: look now, not at the next check
	watched sync.Once

	// Changed fires when a talk starts, ends or fails, and every check while one runs, for the
	// countdown; listeners must not block, and must not call Start, Stop or Toggle themselves.
	Changed hook.Hook[struct{}]
}

var shared = &Feature{poke: make(chan struct{}, 1)}

// A talk has the microphones: no voice turn starts over it, and the action button ends it.
func init() { voice.YieldTo(shared.Busy, shared.Stop) }

func Get() *Feature { return shared }

// Address is where a camera is talked to and the login for it: the Reolink recorder's for a camera on
// it, or the one given on the setup page. False for a camera that has neither, which gets no Talk.
func Address(entity string) (addr, user, pass string, ok bool) {
	if a, u, p, ok := home.ReolinkRTSP(entity); ok {
		return a, u, p, true
	}
	c := config.Get().TalkBack
	if a := c.Cameras[entity]; a != "" {
		return a, c.User, c.Pass, true
	}
	return "", "", "", false
}

// Offered is whether the camera page shows Talk for entity: the switch is on and the camera has an
// address to talk to.
func Offered(entity string) bool {
	if !Here || !config.Get().Security.TalkBack || entity == "" || entity == home.LocalCamera {
		return false
	}
	_, _, _, ok := Address(entity)
	return ok
}

// Seen is the screen saying it has just drawn the camera page for entity with its Talk bar on it.
func (f *Feature) Seen(entity string) {
	f.mu.Lock()
	f.seen, f.seenAt = entity, time.Now()
	f.mu.Unlock()
}

// State is read by the screen each frame.
func (f *Feature) State() State {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.state
	if st.Phase == Talking {
		st.Left = max(time.Until(f.until), 0)
	}
	if st.Error != "" && time.Since(f.failAt) > failShown {
		st.Error = ""
	}
	return st
}

// Busy is whether a talk has the microphones, for the wake word and the action button to leave them
// alone.
func (f *Feature) Busy() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state.Phase != Idle
}

// Toggle is the Talk control: it starts a talk to entity, or ends the one running to it.
func (f *Feature) Toggle(entity string) {
	f.op.Lock()
	defer f.op.Unlock()
	f.mu.Lock()
	running := f.state.Phase != Idle && f.state.Entity == entity
	f.mu.Unlock()
	if running {
		f.stop()
		return
	}
	f.start(entity)
}

// Start talks to entity, ending any talk already running. It returns once the old talk is gone; the
// camera is asked for its backchannel in the background.
func (f *Feature) Start(entity string) {
	f.op.Lock()
	defer f.op.Unlock()
	f.start(entity)
}

// Stop ends the talk running, if any, and waits for the camera to have been let go.
func (f *Feature) Stop() {
	f.op.Lock()
	defer f.op.Unlock()
	f.stop()
}

func (f *Feature) start(entity string) {
	if !Offered(entity) || phone.Get().Busy() {
		return
	}
	f.watch()
	f.stop()
	// A voice turn already listening would hear what is said to the door as a question.
	if voice.Get().Busy() {
		voice.Get().Cancel()
	}
	// The view is held through the asking too: a doorbell's view that was about to close must not
	// close while the camera is still being asked.
	home.Get().HoldCamera(entity, openWait+holdView)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	f.mu.Lock()
	f.gen++
	gen := f.gen
	f.state = State{Entity: entity, Phase: Opening}
	f.cancel, f.done = cancel, done
	f.mu.Unlock()
	f.Changed.Emit(struct{}{})
	safe.Go("talk back", func() {
		err := errors.New("the talk stopped unexpectedly") // what a panic in it leaves behind
		defer func() {
			f.mu.Lock()
			if f.gen == gen {
				f.state.Phase, f.state.Left = Idle, 0
				if err != nil {
					f.state.Error, f.failAt = clip(err.Error()), time.Now()
				}
			}
			f.mu.Unlock()
			close(done)
			f.Changed.Emit(struct{}{})
		}()
		err = f.talk(ctx, gen, entity)
	})
}

func (f *Feature) stop() {
	f.mu.Lock()
	cancel, done := f.cancel, f.done
	f.cancel, f.done = nil, nil
	f.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// watch has everything that can end a talk say so as it happens, on top of the check every second.
func (f *Feature) watch() {
	f.watched.Do(func() {
		poke := func() {
			select {
			case f.poke <- struct{}{}:
			default:
			}
		}
		home.Get().Changed.Listen(func(struct{}) { poke() })
		security.Get().Changed.Listen(func(struct{}) { poke() })
		mute.Get().Changed.Listen(func(bool) { poke() })
		phone.Get().Changed.Listen(func(phone.State) { poke() })
	})
}

// errEnded is a talk that something other than the camera ended; it is not a failure to show.
var errEnded = errors.New("ended")

func (f *Feature) talk(ctx context.Context, gen int, entity string) (err error) {
	addr, user, pass, ok := Address(entity)
	if !ok {
		return errors.New("no address for this camera")
	}
	octx, cancel := context.WithTimeout(ctx, openWait)
	s, err := open(octx, addr, user, pass)
	cancel()
	if ctx.Err() != nil {
		if err == nil {
			s.Close()
		}
		return nil
	}
	if err != nil {
		slog.Warn("talk back: the camera would not take it", "entity", entity, "addr", redacted(addr), "err", err)
		return err
	}
	defer s.Close()

	// Nothing is sent until the screen has shown the red bar: whatever happened while the camera was
	// being asked (the view closed or covered, the switch, a mute) is looked at first.
	talkingAt := time.Now()
	f.mu.Lock()
	f.state.Phase = Talking
	f.until = talkingAt.Add(maxTalk)
	f.mu.Unlock()
	f.Changed.Emit(struct{}{})
	start := time.Now()
	slog.Info("talk back: talking", "entity", entity, "addr", redacted(addr), "codec", s.Codec())
	defer func() {
		why := "ended"
		if err != nil && err != errEnded {
			why = err.Error()
		}
		slog.Info("talk back: done", "entity", entity, "after", time.Since(start).Round(time.Second), "why", why)
		if err == errEnded {
			err = nil
		}
	}()
	for {
		if why := f.over(entity); why != "" {
			slog.Info("talk back: not starting", "why", why)
			return errEnded
		}
		f.mu.Lock()
		shown := f.seen == entity && f.seenAt.After(talkingAt)
		f.mu.Unlock()
		if shown {
			break
		}
		if time.Since(talkingAt) > seenWithin {
			slog.Info("talk back: not starting", "why", "the camera page is not on the screen")
			return errEnded
		}
		select {
		case <-ctx.Done():
			return errEnded
		case <-time.After(20 * time.Millisecond):
		}
	}

	// Music, the radio and the house's music turned down while the room talks to the door, as for a
	// call: what is left of them after the echo canceller would go out with every word. The camera's
	// own sound is not background and stays as it is.
	duckRoom(true)
	defer duckRoom(false)

	frames, stop := mic.Get().Listen("talk back")
	defer stop()
	d := halfrate.NewDown()
	t := time.NewTicker(check)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return errEnded
		case <-s.Done():
			return errors.New("the camera hung up")
		case fr, ok := <-frames:
			if !ok {
				return errors.New("the microphones stopped")
			}
			if err := s.Write(d.Run(quieter(fr))); err != nil {
				return err
			}
		case <-f.poke:
			if why := f.over(entity); why != "" {
				slog.Info("talk back: ending", "why", why)
				return errEnded
			}
		case <-t.C:
			if why := f.over(entity); why != "" {
				slog.Info("talk back: ending", "why", why)
				return errEnded
			}
			f.mu.Lock()
			seen := f.seen == entity && time.Since(f.seenAt) < seenWithin
			f.mu.Unlock()
			if !seen {
				slog.Info("talk back: ending", "why", "the camera page is not on the screen")
				return errEnded
			}
			home.Get().HoldCamera(entity, holdView)
			f.Changed.Emit(struct{}{}) // the countdown
		}
	}
}

// quieter is a frame turned down to talkLevel, in a new slice: the one heard is shared with every
// other listener.
func quieter(frame []int16) []int16 {
	out := make([]int16, len(frame))
	for i, v := range frame {
		out[i] = int16(float32(v) * talkLevel)
	}
	return out
}

// duckRoom turns the background down, or lets it back up; a variable for the tests, which have no
// speaker.
var duckRoom = func(on bool) {
	if s := speaker.Sound(); s != nil {
		s.Backgrounds().Duck("talk back", on)
	}
}

// open is onvifback.Open, and a variable so that a test can put a camera of its own behind it.
var open = func(ctx context.Context, addr, user, pass string) (session, error) {
	s, err := onvifback.Open(ctx, addr, user, pass)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// session is what a talk needs of lib/onvifback's.
type session interface {
	Write([]int16) error
	Codec() string
	Done() <-chan struct{}
	Close() error
}

// over is why a running talk to entity has to end, besides the camera, a tap or the screen: empty
// while it may go on.
func (f *Feature) over(entity string) string {
	if !config.Get().Security.TalkBack {
		return "switched off"
	}
	if v, up := home.Get().Camera(); !up || v.Entity != entity {
		return "the camera page closed"
	}
	// A mute that cannot be read is taken as one: a talk is the room going out of the house.
	if m, err := mute.Get().Muted(); err != nil || m {
		return "the microphones were muted"
	}
	if phone.Get().Busy() {
		return "a call"
	}
	f.mu.Lock()
	late := time.Now().After(f.until)
	f.mu.Unlock()
	if late {
		return "two minutes passed"
	}
	return ""
}

// clip keeps a failure short enough for the camera page.
func clip(s string) string {
	if r := []rune(s); len(r) > maxError {
		return string(r[:maxError-1]) + "…"
	}
	return s
}

// redacted is an address fit for the log: without a login it may carry.
func redacted(addr string) string {
	u, err := url.Parse(addr)
	if err != nil {
		return "(an address that does not parse)"
	}
	u.User = nil
	return u.String()
}
