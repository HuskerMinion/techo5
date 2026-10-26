package takes

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// fake stands in for everything the recorder asks of the rest of the device: a clock that moves only
// when told, a microphone the test speaks into, and each thing that has the microphones a better claim.
type fake struct {
	mu    sync.Mutex
	clock time.Time

	on, muted, muteUnread, call, talk, silent, announcing, ringing, turn, audible atomic.Bool
	canceled, chimes                                                              atomic.Int32

	// down and downAt are the action button: down, and when it went down.
	down   atomic.Bool
	downAt atomic.Int64

	// heardUntil is when the last chime stops being heard: the note and the echo canceller's hold on it
	// (hardware/mic), a second in all, through which the device sounds like it is playing something.
	heardUntil atomic.Int64

	mics chan *fakeMic
}

// fakeMic is one recording's listener: the test sends frames into it.
type fakeMic struct {
	frames  chan []int16
	stopped chan struct{}
}

// newDevice is a device with the switch on, nothing else going on, and a recorder of its own.
func newDevice(t *testing.T) (*fake, *Feature) {
	d := &fake{clock: time.Date(2026, 10, 26, 18, 0, 0, 0, time.UTC), mics: make(chan *fakeMic, 1)}
	d.on.Store(true)
	f := &Feature{poke: make(chan struct{}, 1), dev: device{
		listen: func(string) (<-chan []int16, func()) {
			m := &fakeMic{frames: make(chan []int16), stopped: make(chan struct{})}
			d.mics <- m
			var once sync.Once
			return m.frames, func() { once.Do(func() { close(m.stopped) }) }
		},
		switchedOn: d.on.Load,
		muted: func() (bool, error) {
			if d.muteUnread.Load() {
				return false, errors.New("the mute could not be read")
			}
			return d.muted.Load(), nil
		},
		inCall:     d.call.Load,
		talking:    d.talk.Load,
		silent:     d.silent.Load,
		announcing: d.announcing.Load,
		ringing:    d.ringing.Load,
		turnOpen:   d.turn.Load,
		cancelTurn: func() { d.canceled.Add(1); d.turn.Store(false) },
		audible:    func() bool { return d.audible.Load() || d.now().UnixNano() < d.heardUntil.Load() },
		chime: func(...speaker.Note) {
			d.chimes.Add(1)
			d.heardUntil.Store(d.now().Add(time.Second).UnixNano())
		},
		now:      d.now,
		pressing: func() (bool, time.Time) { return d.down.Load(), time.Unix(0, d.downAt.Load()) },
	}, epoch: d.clock}
	f.watched.Do(func() {}) // the real mute, phone and the rest are not listened to
	return d, f
}

func (d *fake) now() time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.clock
}

func (d *fake) wait(by time.Duration) {
	d.mu.Lock()
	d.clock = d.clock.Add(by)
	d.mu.Unlock()
}

// recording is asked for and started with a press, and hands back the microphone it records from.
func (d *fake) recording(t *testing.T, f *Feature, word string) *fakeMic {
	t.Helper()
	if err := f.Arm(word, Word, "browser"); err != nil {
		t.Fatalf("asking for a recording: %v", err)
	}
	if !f.Begin(0) {
		t.Fatal("the press did not start the recording")
	}
	select {
	case m := <-d.mics:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("the recording never listened")
	}
	return nil
}

// frame is one frame of a sound at level: a tone for a word, a low hiss for the room.
func frame(level float64, n int) []int16 {
	f := make([]int16, mic.FrameSamples)
	for i := range f {
		f[i] = int16(level * math.Sin(float64(n*mic.FrameSamples+i)*0.31))
	}
	return f
}

// say sends n frames at level, the clock moving a frame's time with each, and reports whether the
// recording took them all.
func (d *fake) say(m *fakeMic, level float64, n int) bool {
	for i := range n {
		d.wait(frameTime)
		select {
		case m.frames <- frame(level, i):
		case <-m.stopped:
			return false
		case <-time.After(5 * time.Second):
			return false
		}
	}
	return true
}

// room is the room's own sound, and word one take of the word.
const (
	room = 40.0
	word = 4000.0
)

// takesOf says the word n times into a recording, a second and a half apart, after the lead-in.
func (d *fake) takesOf(t *testing.T, m *fakeMic, n int) {
	t.Helper()
	if !d.say(m, room, int(leadIn/frameTime)+50) {
		t.Fatal("the recording stopped in the lead-in")
	}
	for range n {
		if !d.say(m, word, 25) || !d.say(m, room, 50) {
			t.Fatal("the recording stopped taking frames")
		}
	}
}

// settled waits for the recorder to come to phase.
func settled(t *testing.T, f *Feature, phase Phase) State {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := f.State("browser")
		if st.Phase == phase {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("the recorder is %v, want %v (why %q)", st.Phase, phase, st.Why)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// stopped waits for the recording to have let go of the microphone.
func stopped(t *testing.T, m *fakeMic) {
	t.Helper()
	select {
	case <-m.stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the recording never let go of the microphone")
	}
}

// press puts the action button down now, and lift lets it up once every listener has heard it.
func (d *fake) press() {
	d.downAt.Store(d.now().UnixNano())
	d.down.Store(true)
}

func (d *fake) lift() { d.down.Store(false) }
