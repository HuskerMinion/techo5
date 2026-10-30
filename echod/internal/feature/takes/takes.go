// Package takes records takes of a word on the device's own microphones, for training a wake word
// model on what this device really hears (docs/training-wake-word.md: wakesmith's real voices, or
// step 4 by hand). The setup page asks for a recording, a press on the device starts it, and it runs
// for up to two minutes while somebody says the word over and over. Then it is cut into one take per
// word, and the takes wait in memory for the setup page to download them as a zip of 16 kHz mono WAVs.
//
// It is a new use of the microphones, so it has a switch of its own, Record takes of a word, under
// Privacy & Security on the screen and in Home Assistant, off on a new device. On top of the switch,
// every recording needs a press at the device: the action button on a Dot, Start on the screen of a
// Show or a Spot. A browser alone can ask for a recording but never start one, and nothing else can
// ask at all: there is no entity, action or remote API route that starts one.
//
// Nothing is written to the device's storage. A recording is thrown away by a mute, a call, a talk
// through a camera or an announcement starting, and the takes are gone when they are thrown away,
// when another recording replaces them, when the setup page closes, or when the device restarts.
//
// Kept apart from feature/setup and feature/detect, which both need it, and which it must never call
// back into: setup asks it for the press and the takes, and detect for whether a word heard now is a
// take and not the word said to the device.
package takes

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
	"github.com/HuskerMinion/techo5/echod/internal/feature/talkback"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
	"github.com/HuskerMinion/techo5/echod/internal/lib/micsilent"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

const (
	// armFor is how long a recording asked for on the setup page waits for the press on the device, as
	// long as the page waits for the press that lets a browser in.
	armFor = 60 * time.Second

	// tries is how many recordings may be asked for in a row with no press to start them before asking
	// is refused for coolOff, as the page's own asking to be let in is (feature/setup). In a row is each
	// within coolOff of the one before. While one waits, a Dot's next tap starts it rather than a
	// question: a browser asking over and over must not keep the device that way.
	tries   = 3
	coolOff = 10 * time.Minute

	// Longest is the ceiling: twenty to thirty takes said a second or two apart fit in it with room to
	// spare, and a recording left running by mistake does not run for long.
	Longest = 2 * time.Minute

	// tail is how long after a recording ends the microphones are still the takes': a word crossing
	// its threshold just after Stop (the person is still mid-take) arrives up to the engine's hold and
	// refractory time later (feature/detect engine.go), and must not open a turn either.
	tail = 1100 * time.Millisecond

	// tailSlack is how long past the tail the end of it is said, so that whoever asks Holding then is
	// told it is over.
	tailSlack = 50 * time.Millisecond

	// ownHeard is how long after one of the recorder's own notes (asked for, started, stopped) the device
	// still sounds like it is playing something: the note (120 ms), the playback buffer sounding on after
	// it (speaker.HardwareTail, 150 ms), and the echo canceller's hold on what it last played (a second,
	// hardware/mic cancel.go), with a little to spare. The recorder's own note is not something playing.
	ownHeard = 1400 * time.Millisecond

	// leadIn is dropped from the front: the start chime is still leaving the speaker, and its echo is
	// loud enough to pass for a take.
	leadIn = 700 * time.Millisecond

	// gap is how long without a frame ends a recording, keeping what came before: the capture
	// restarting keeps its listeners but leaves a hole, and takes either side of it are not one
	// recording any more.
	gap = time.Second

	// check is how often a recording, or a wait for the press, looks at what ends it, on top of looking
	// whenever one of those things says it changed.
	check = 250 * time.Millisecond

	// MaxWord is the most of a word the setup page takes, in characters.
	MaxWord = 40
)

// Phase is where the recorder is.
type Phase int

const (
	Idle      Phase = iota
	Armed           // asked for on the setup page, waiting for the press on the device
	Recording       // the microphones are going into the takes
	Kept            // cut into takes, waiting to be downloaded or thrown away
)

func (p Phase) String() string {
	switch p {
	case Armed:
		return "armed"
	case Recording:
		return "recording"
	case Kept:
		return "kept"
	}
	return "idle"
}

// Kind is what the takes are of: the word, or something that sounds like it, for a model's negatives.
type Kind int

const (
	Word Kind = iota
	NotWord
)

func (k Kind) String() string {
	if k == NotWord {
		return "not the word"
	}
	return "word"
}

// Card is what the screen shows while the recorder needs somebody at the device: the Start card while
// it waits for the press, and the recording with its Stop while it runs.
type Card struct {
	ID      uint64 // the asking a Start answers, for Begin
	Phase   Phase
	Word    string
	Kind    Kind
	Elapsed time.Duration // of the recording, while it runs
}

// Up is whether there is a card to show.
func (c Card) Up() bool { return c.Phase == Armed || c.Phase == Recording }

// State is what the setup page shows.
type State struct {
	Phase   Phase
	Mine    bool // the browser asking is the one that asked for the recording
	Word    string
	Kind    Kind
	Elapsed time.Duration // recorded so far, or in all once kept
	About   int           // takes heard so far, while recording
	Takes   int           // kept
	LeftOut []LeftOut     // what was heard and not kept, once kept
	Lost    bool          // frames went missing: listen before using these
	Bytes   int           // the zip's size, about
	Why     string        // why the last recording ended, or was not started or kept
}

// LeftOut is something heard that is not a take, and why.
type LeftOut struct {
	At, Length time.Duration
	Why        string
}

type Feature struct {
	// op makes Arm and Begin one at a time: two presses close together must not both start a recording.
	// Stop and Discard never take it, since the button and the setup page call them from places that
	// must not wait.
	op sync.Mutex

	mu         sync.Mutex
	phase      Phase
	id         uint64 // the asking; anything older that comes back to change the state is ignored
	word       string
	kind       Kind
	session    string // the setup page's browser that asked, which alone may see and take the takes
	armedUntil time.Time
	took       time.Time // when the press that started the recording was taken
	ownUntil   time.Time // until when the recorder's own last note may still be heard (ownHeard)
	why        string
	started    time.Time
	elapsed    time.Duration
	about      int
	lost       bool
	mic        config.Microphone // what shaped the sound, for takes.json
	cancel     context.CancelFunc
	finish     chan struct{}

	// unanswered is the recordings asked for in a row with no press, and shutUntil when asking is
	// allowed again once there have been tries of them. lastAsked is when the latest of them was asked
	// for: a run that went quiet for coolOff is over.
	unanswered int
	shutUntil  time.Time
	lastAsked  time.Time

	// What was kept: the recording, the takes cut from it and what was left out of them.
	pcm  []int16
	cuts []span
	left []leftOut

	// running and tailUntil are read on every frame of the wake word's stream to Home Assistant
	// (through voice.YieldTo) and by detect for every word heard, so they are not behind mu. running is
	// the asking whose recording has the microphones, 0 for none: a recording still winding down after
	// it was thrown away cannot let go of the next one's. tailUntil is on the clock since epoch, which
	// a clock set back does not move.
	running   atomic.Uint64
	tailUntil atomic.Int64
	epoch     time.Time

	poke    chan struct{} // something that can end a recording changed: look now, not at the next check
	watched sync.Once

	// dev is what the recorder asks of the rest of the device.
	dev device

	// Changed fires when the recorder is asked for, starts, ends or is thrown away, and every second
	// while it records; listeners must not block, and must not call Arm or Begin.
	Changed hook.Hook[struct{}]
}

var shared = &Feature{poke: make(chan struct{}, 1), dev: thisDevice, epoch: time.Now()}

// The takes have the microphones while a recording runs: no turn starts over it, nearest does not
// claim a word said into it, Home Assistant's wake word stream stops, and the action button ends it.
func init() { voice.YieldTo("takes being recorded", shared.Holding, shared.Stop) }

func Get() *Feature { return shared }

// device is what the recorder asks of the rest of the device: the microphones, everything that has
// them a better claim, the action button, the speaker for the chimes, and the clock. A field of the
// recorder's rather than the package's, so that a test's recorder, and whatever it leaves running, has
// its own.
type device struct {
	listen     func(name string) (<-chan []int16, func())
	switchedOn func() bool
	muted      func() (bool, error)
	inCall     func() bool
	talking    func() bool
	silent     func() bool
	announcing func() bool
	ringing    func() bool
	turnOpen   func() bool
	cancelTurn func()
	chime      func(notes ...speaker.Note)
	now        func() time.Time

	// pressing is whether the action button is down, and when it last went down. It reads down until
	// every listener has heard it come up (hardware/buttons).
	pressing func() (down bool, at time.Time)

	// audible is whether the device is making a sound the microphones would carry into the takes.
	audible func() bool
}

var thisDevice = device{
	listen:     func(name string) (<-chan []int16, func()) { return mic.Get().Listen(name) },
	switchedOn: func() bool { return config.Get().Security.Takes },
	muted:      func() (bool, error) { return mute.Get().Muted() },
	inCall:     func() bool { return phone.Get().Busy() },
	talking:    func() bool { return talkback.Get().Busy() },
	silent:     micsilent.Silent,
	announcing: func() bool { return announce.Get().Recording() },
	ringing:    ring.IsSounding,
	turnOpen:   func() bool { return voice.Get().Busy() },
	cancelTurn: func() { voice.Get().Cancel() },
	chime: func(notes ...speaker.Note) {
		speaker.Sound().Interject(func(p *speaker.Player) { p.Chime(chimeLevel, notes...) })
	},
	now:      time.Now,
	pressing: func() (bool, time.Time) { return buttons.Get().Pressed(buttons.Action) },

	// Something holding the speaker (an announcement, a reply), anything else on its way out of it (the
	// music, the radio, a chime or a doorbell's ding-dong), or the echo canceller working. Not the
	// canceller alone: it runs only with echo cancellation switched on. Not the speaker's backgrounds
	// either: a paused track keeps its place there.
	audible: func() bool {
		if mic.Get().Canceling() {
			return true
		}
		s, p := speaker.Sound(), speaker.Get()
		return (s != nil && s.Busy()) || (p != nil && p.Sounding())
	},
}

// The notes that say the device is asked for a recording, that one has started, and that it has
// stopped. The second two are announce's, since they mean the same thing, a microphone that is open
// and then not.
var (
	askNotes   = []speaker.Note{{Freq: 660, Ms: 120}}
	startNotes = []speaker.Note{{Freq: 587, Ms: 90}, {Freq: 880, Ms: 140}}
	stopNotes  = []speaker.Note{{Freq: 880, Ms: 90}}
)

// chimeLevel is how loud the chimes are: heard by whoever is recording, not across the house at night.
const chimeLevel = 0.35

// errOff is a recording asked for with the switch off.
var errOff = errors.New("Record takes of a word is off. Turn it on under Privacy & Security, on the device's screen or in Home Assistant")

// Arm asks for a recording of word for the browser session: it waits a minute for the press on the
// device, and says so at the device with a note. Asked again by the same browser while it waits, the
// word changes and the minute does not. Any takes the browser kept are thrown away, since a new
// recording replaces them; another browser's are not, and nor can it ask over them.
func (f *Feature) Arm(word string, kind Kind, session string) error {
	f.op.Lock()
	defer f.op.Unlock()
	word = Clean(word)
	switch {
	case word == "":
		return errors.New("type the word first")
	case !layout.HasMicrophone():
		return errors.New("this device has no microphones")
	case !f.dev.switchedOn():
		return errOff
	}
	f.watch()
	if why := f.refusal(); why != "" {
		return errors.New(why)
	}
	if f.dev.turnOpen() {
		return errors.New("a request is running; try again when it is over")
	}
	t := f.dev.now()
	f.mu.Lock()
	switch {
	case f.phase != Idle && f.session != session:
		f.mu.Unlock()
		if f.phase == Kept {
			return errors.New("another browser's takes are kept on this device until it throws them away or the setup page closes")
		}
		return errors.New("another browser is recording takes on this device")
	case f.phase == Recording:
		f.mu.Unlock()
		return errors.New("a recording is running; stop it first")
	case f.phase == Armed:
		// Another word is another card: a Start tapped on the one with the old word on it starts
		// nothing. The minute goes on as it was.
		f.id++
		id := f.id
		f.word, f.kind = word, kind
		f.mu.Unlock()
		f.Changed.Emit(struct{}{})
		safe.Go("takes waiting for the press", func() { f.waitPress(id) })
		return nil
	case t.Before(f.shutUntil):
		f.mu.Unlock()
		return errors.New("recording was asked for too many times with nobody pressing on the device; try again in a few minutes")
	}
	// Askings spread out are not a browser keeping the device waiting: three over a day would otherwise
	// shut out the next one.
	if t.Sub(f.lastAsked) >= coolOff {
		f.unanswered = 0
	}
	f.lastAsked = t
	f.unanswered++
	if f.unanswered > tries {
		f.unanswered, f.shutUntil = 0, t.Add(coolOff)
		f.mu.Unlock()
		slog.Warn("takes: asked for too many times with no press; not asking again for a while", "for", coolOff)
		return errors.New("recording was asked for too many times with nobody pressing on the device; try again in a few minutes")
	}
	f.id++
	id := f.id
	f.phase, f.word, f.kind, f.session = Armed, word, kind, session
	f.armedUntil, f.why = t.Add(armFor), ""
	f.forget()
	f.mu.Unlock()
	slog.Info("takes: asked for, waiting for the press on the device", "kind", kind)
	f.note(askNotes...)
	f.Changed.Emit(struct{}{})
	safe.Go("takes waiting for the press", func() { f.waitPress(id) })
	return nil
}

// Clean is a typed word made fit to keep: spaces trimmed and run together, control characters
// taken out, and at most MaxWord characters.
func Clean(word string) string {
	word = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, word)
	word = strings.Join(strings.Fields(word), " ")
	if r := []rune(word); len(r) > MaxWord {
		word = strings.TrimSpace(string(r[:MaxWord]))
	}
	return word
}

// waitPress gives the asking id up when the minute runs out, or as soon as it could not start.
func (f *Feature) waitPress(id uint64) {
	t := time.NewTicker(check)
	defer t.Stop()
	for {
		f.mu.Lock()
		waiting := f.phase == Armed && f.id == id
		late := f.dev.now().After(f.armedUntil)
		f.mu.Unlock()
		if !waiting {
			return
		}
		why := f.refusal()
		if why == "" && late {
			why = "nobody pressed on the device within the minute"
		}
		if why != "" {
			f.giveUp(id, why)
			return
		}
		select {
		case <-f.poke:
		case <-t.C:
		}
	}
}

// refusal is why a recording cannot start, or must stop waiting for the press: empty when it can.
// Everything it asks fails closed, but for the recorder's own notes (asked for, started, stopped): they
// are not something playing, for as long as they may be heard (ownHeard).
func (f *Feature) refusal() string {
	t := f.dev.now()
	f.mu.Lock()
	own := t.Before(f.ownUntil)
	f.mu.Unlock()
	if !f.dev.switchedOn() {
		return "Record takes of a word was switched off"
	}
	// A mute that cannot be read is taken as one: the microphones are what this is about.
	if m, err := f.dev.muted(); err != nil || m {
		return "the microphones are muted"
	}
	switch {
	case f.dev.inCall():
		return "a call has the microphones"
	case f.dev.talking():
		return "a talk through a camera has the microphones"
	case f.dev.silent():
		return "the microphones are silent"
	case f.dev.announcing():
		return "an announcement is being recorded"
	case f.dev.ringing():
		return "an alarm or timer is ringing"
	case !own && f.dev.audible():
		return "something is playing; pause or stop it first, or the takes would carry it"
	}
	return ""
}

// Owner is the browser session the recorder's asking, recording or takes are for, or "" when it has
// none: for the setup page to know when that browser has gone (feature/setup takes.go).
func (f *Feature) Owner() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.phase == Idle {
		return ""
	}
	return f.session
}

// giveUp ends the wait for the press of asking id, saying why.
func (f *Feature) giveUp(id uint64, why string) {
	f.mu.Lock()
	if f.phase != Armed || f.id != id {
		f.mu.Unlock()
		return
	}
	f.phase, f.why = Idle, why
	f.mu.Unlock()
	slog.Info("takes: not started", "why", why)
	f.Changed.Emit(struct{}{})
}

// TookPress is whether the press of the action button being handled now is the recorder's: while it
// waits for one, and while the press that started the recording is still being handled. The button's
// listeners run in no fixed order, and voice may ask after that press has already started it; the
// button reads down until every listener has heard it, so this is that press however long starting
// took. Not while a ring sounds or a call rings: the press is for those first, and the wait gives up
// on them anyway.
func (f *Feature) TookPress() bool {
	busy := f.dev.ringing() || f.dev.inCall()
	down, at := f.dev.pressing()
	t := f.dev.now()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.phase == Armed && t.Before(f.armedUntil) && !busy {
		return true
	}
	return down && !f.took.IsZero() && !f.took.Before(at)
}

// Pressed is the action button's tap, on a device that has one: it starts the recording that is
// waiting for it, if one is.
func (f *Feature) Pressed() bool {
	if f.dev.ringing() || f.dev.inCall() {
		return false
	}
	return f.Begin(0)
}

// Begin is the press on the device: it starts the recording asked for as id, or whichever is waiting
// for 0. It reports whether there was one to take the press; one that cannot start now says why on
// the setup page.
func (f *Feature) Begin(id uint64) bool {
	f.op.Lock()
	defer f.op.Unlock()
	t := f.dev.now()
	f.mu.Lock()
	if f.phase != Armed || (id != 0 && id != f.id) || !t.Before(f.armedUntil) {
		f.mu.Unlock()
		return false
	}
	id = f.id
	f.took = t
	f.unanswered = 0 // somebody is at the device
	kind := f.kind
	f.mu.Unlock()

	if why := f.refusal(); why != "" {
		f.giveUp(id, why)
		return true
	}
	// The microphones are the takes' from here: no turn opens on them now, so one that slipped in
	// between the asking and the press is the only one there can be, and it is ended.
	f.running.Store(id)
	if f.dev.turnOpen() {
		f.dev.cancelTurn()
	}
	ctx, cancel := context.WithCancel(context.Background())
	finish := make(chan struct{}, 1)
	f.mu.Lock()
	if f.phase != Armed || f.id != id {
		f.mu.Unlock()
		cancel()
		f.release(id)
		return true
	}
	f.phase, f.started, f.elapsed, f.about, f.lost = Recording, t, 0, 0, false
	f.cancel, f.finish = cancel, finish
	f.mic = config.Get().Microphone
	f.mu.Unlock()
	slog.Info("takes: recording", "kind", kind, "at most", Longest)
	f.Changed.Emit(struct{}{})
	safe.Go("takes recording", func() { f.record(ctx, id, finish) })
	return true
}

// Decline is Not now on the screen's Start card.
func (f *Feature) Decline(id uint64) { f.giveUp(id, "not started at the device") }

// Stop ends what is running: a recording keeps its takes, and a wait for the press is called off. It
// never waits, since the action button calls it.
func (f *Feature) Stop() {
	f.mu.Lock()
	phase, id, finish := f.phase, f.id, f.finish
	f.mu.Unlock()
	switch phase {
	case Armed:
		f.giveUp(id, "called off")
	case Recording:
		select {
		case finish <- struct{}{}:
		default:
		}
	}
}

// Discard throws away whatever the recorder has, saying why: a wait, a recording or takes. It never
// waits, since the setup page closing calls it from the button and from Home Assistant's switch.
func (f *Feature) Discard(why string) {
	f.mu.Lock()
	was, cancel, old := f.phase, f.cancel, f.id
	f.id++ // a recording still winding down, or a download still writing, leaves the takes alone
	f.phase, f.cancel = Idle, nil
	if was != Idle {
		f.why = why
	}
	f.forget()
	f.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if was == Recording {
		f.release(old)
	}
	if was != Idle {
		slog.Info("takes: thrown away", "was", was, "why", why)
		f.Changed.Emit(struct{}{})
	}
}

// forget lets go of the takes kept. With mu held.
func (f *Feature) forget() {
	f.pcm, f.cuts, f.left, f.lost = nil, nil, nil, false
}

// since is the recorder's own clock: how long since it was made, on the monotonic clock where there is
// one, so that the tail is not lengthened by the clock being set back.
func (f *Feature) since() time.Duration { return f.dev.now().Sub(f.epoch) }

// release gives the microphones back from the recording asked for as id, if they are still its, once
// the tail has passed, and says so when it has.
func (f *Feature) release(id uint64) {
	f.tailUntil.Store(int64(f.since() + tail))
	if f.running.CompareAndSwap(id, 0) {
		f.tailOver(0)
	}
}

// tailOver says the tail is over once it is: a timer runs on the monotonic clock, which a slewing wall
// clock can leave a moment behind, so it looks again while Holding still says the tail is on.
func (f *Feature) tailOver(tries int) {
	time.AfterFunc(tail+tailSlack, func() {
		if f.running.Load() == 0 && f.Holding() && tries < 20 {
			f.tailOver(tries + 1)
			return
		}
		f.Changed.Emit(struct{}{})
	})
}

// Holding is whether the microphones are the takes': while a recording runs, and for the tail after.
// Read on the audio path, so it takes no lock.
func (f *Feature) Holding() bool {
	return f.running.Load() != 0 || f.since() < time.Duration(f.tailUntil.Load())
}

// Card is read by the screen each frame.
func (f *Feature) Card() Card {
	t := f.dev.now()
	f.mu.Lock()
	defer f.mu.Unlock()
	switch f.phase {
	case Armed:
		if t.Before(f.armedUntil) {
			return Card{ID: f.id, Phase: Armed, Word: f.word, Kind: f.kind}
		}
	case Recording:
		return Card{ID: f.id, Phase: Recording, Word: f.word, Kind: f.kind, Elapsed: t.Sub(f.started)}
	}
	return Card{}
}

// State is what the setup page shows the browser session.
func (f *Feature) State(session string) State {
	t := f.dev.now()
	f.mu.Lock()
	defer f.mu.Unlock()
	st := State{Phase: f.phase, Mine: session != "" && session == f.session}
	if !st.Mine {
		return st
	}
	st.Word, st.Kind, st.Why, st.About, st.Lost = f.word, f.kind, f.why, f.about, f.lost
	switch f.phase {
	case Recording:
		st.Elapsed = t.Sub(f.started)
	case Kept:
		st.Elapsed, st.Takes, st.Bytes = f.elapsed, len(f.cuts), zipSize(f.word, f.kind, f.cuts)
		for _, l := range f.left {
			st.LeftOut = append(st.LeftOut, LeftOut{At: frames(l.from), Length: frames(l.to - l.from), Why: l.why})
		}
	}
	return st
}

// watch has everything that can end a recording say so as it happens, on top of the check.
func (f *Feature) watch() {
	f.watched.Do(func() {
		poke := func() {
			select {
			case f.poke <- struct{}{}:
			default:
			}
		}
		mute.Get().Changed.Listen(func(bool) { poke() })
		phone.Get().Changed.Listen(func(phone.State) { poke() })
		talkback.Get().Changed.Listen(func(struct{}) { poke() })
		announce.Get().Changed.Listen(func(struct{}) { poke() })
		micsilent.Changed.Listen(func(bool) { poke() })
		// The switch going off throws away the takes too, not only a recording: it is the owner taking
		// the permission back.
		security.Get().Changed.Listen(func(struct{}) {
			poke()
			if !f.dev.switchedOn() {
				f.Discard("Record takes of a word was switched off")
			}
		})
	})
}

// note plays one of the recorder's own notes, which for as long as it may be heard is not something
// playing (ownHeard): asking again straight after a recording ended is not refused for its stop chime.
// Never with mu held.
func (f *Feature) note(notes ...speaker.Note) {
	t := f.dev.now()
	f.mu.Lock()
	f.ownUntil = t.Add(ownHeard)
	f.mu.Unlock()
	f.dev.chime(notes...)
}
