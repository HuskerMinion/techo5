package mute

import (
	"errors"
	"sync"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/privacy"
)

// watched is a mute with its button and what it announces stood in for, so that a test can press
// the button and count what gets published.
type watched struct {
	*Mute
	mu        sync.Mutex
	down      bool
	went      time.Time
	published []bool
}

func watch(line privacy.Mute, muted bool) *watched {
	w := &watched{Mute: &Mute{sw: &esphome.Switch{}, line: line}}
	w.sw.Set(muted)
	w.button = func() (bool, time.Time) {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.down, w.went
	}
	w.announce = func(muted, asked bool) {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.published = append(w.published, muted)
	}
	return w
}

func (w *watched) press(down bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.down = down
	if down {
		w.went = time.Now()
	}
}

func (w *watched) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.published)
}

// looks runs n looks of the watch after a hold, one after another.
func (w *watched) looks(prev look, n int) look {
	for range n {
		prev = w.look(prev)
	}
	return prev
}

// After a hold the switch follows the line, however the line moved: the gating driver's own set on
// a long press, a release, or a release and then the driver's set. Before, a hold that set the latch
// left the switch saying live, and the next tap set the latch again instead of letting it go (#153).
func TestFollowAfterAHold(t *testing.T) {
	for _, tc := range []struct {
		name      string
		was       bool
		states    []bool // what the line reads at each look
		published int
		want      bool
	}{
		{"a hold the driver took as a mute is published", false, []bool{false, true, true}, 1, true},
		{"a hold that released the latch is published", true, []bool{false, false}, 1, false},
		{"a release and then the driver's own set are both published", true, []bool{false, false, true, true}, 2, true},
		{"a hold that changed nothing publishes nothing", true, []bool{true, true}, 0, true},
		{"a line read once out of step is not published", false, []bool{true, false, false}, 0, false},
	} {
		line := &fakeLine{}
		w := watch(line, tc.was)
		var prev look
		for _, s := range tc.states {
			line.muted = s
			prev = w.look(prev)
		}

		if w.count() != tc.published {
			t.Errorf("%s: published %d times, want %d", tc.name, w.count(), tc.published)
		}
		if w.sw.Get() != tc.want {
			t.Errorf("%s: switch says %v, want %v", tc.name, w.sw.Get(), tc.want)
		}
	}
}

// movable is a line a test moves the way the hardware would, and that counts the moves made by the
// daemon.
type movable interface {
	privacy.Mute
	set(muted bool)
	toggled() int
}

func (f *fakeLine) set(muted bool) { f.muted = muted }
func (f *fakeLine) toggled() int   { return f.toggles }

// gatingLine is Amazon's gating driver: its button releases a set latch and does nothing to a live
// one, so a press has already acted only when the microphones were cut.
type gatingLine struct{ fakeLine }

func (g *gatingLine) HardwareActs(was bool) bool { return was }

// A tap during the watch after a hold is published once, by the tap. The hardware moves the line as
// the button goes down and the tap only arrives when it comes up; the watch published the move first,
// so the tap started from the state it had just caused. On a Show it set the latch straight back -
// the unmute sound and then the mute sound, still muted (#153) - and elsewhere it played its sound
// twice.
func TestTapDuringTheWatchIsPublishedOnce(t *testing.T) {
	for _, tc := range []struct {
		name string
		line movable
		was  bool
	}{
		{"a Show's tap that releases the latch", &gatingLine{}, true},
		{"a tap where the hardware acts, unmuting", &fakeLine{acts: true}, true},
		{"a tap where the hardware acts, muting", &fakeLine{acts: true}, false},
	} {
		tc.line.set(tc.was)
		w := watch(tc.line, tc.was)
		prev := w.looks(look{}, 2)

		// The press: the hardware moves the line at once, and the watch looks a few times before the
		// button comes up and the tap arrives.
		w.press(true)
		tc.line.set(!tc.was)
		prev = w.looks(prev, 3)
		w.Toggle()
		w.press(false)
		w.looks(prev, 3)

		if w.count() != 1 {
			t.Errorf("%s: published %d times, want once", tc.name, w.count())
		}
		if w.sw.Get() != !tc.was {
			t.Errorf("%s: switch says %v, want %v", tc.name, w.sw.Get(), !tc.was)
		}
		if n := tc.line.toggled(); n != 0 {
			t.Errorf("%s: the tap moved the line %d times after the hardware had", tc.name, n)
		}
	}
}

// lockedLine is a fakeLine that another goroutine may move.
type lockedLine struct {
	mu sync.Mutex
	fakeLine
}

func (l *lockedLine) Get() (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fakeLine.Get()
}

func (l *lockedLine) set(muted bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.muted = muted
}

// A hold during the watch moves its end on. Before, the second hold found the first watch running
// and was dropped, so what it did after the first watch ended was never published.
func TestASecondHoldExtendsTheWatch(t *testing.T) {
	defer func(f, p time.Duration) { followFor, followPoll = f, p }(followFor, followPoll)
	followFor, followPoll = time.Second, 5*time.Millisecond

	line := &lockedLine{}
	w := watch(line, false)
	done := make(chan struct{})
	go func() { w.followHold(); close(done) }()

	time.Sleep(500 * time.Millisecond)
	w.followHold() // the second hold, which finds the first watch running
	time.Sleep(750 * time.Millisecond)
	line.set(true) // past the first watch's end, inside the second's
	<-done

	if w.count() != 1 || !w.sw.Get() {
		t.Errorf("published %d times, switch says %v; want once, muted", w.count(), w.sw.Get())
	}
}

// slowLine moves when asked and then takes a while to report done, as the Show's latch does, with
// the watch after a hold looking in the meantime.
type slowLine struct {
	fakeLine
	during func()
}

func (s *slowLine) Set(muted bool) error {
	err := s.fakeLine.Set(muted)
	s.during()
	return err
}

// A request from Home Assistant during the watch is published once, by the request: the watch does
// not publish the move it sees while the request is still waiting on the line.
func TestSetDuringTheWatchIsPublishedOnce(t *testing.T) {
	line := &slowLine{}
	w := watch(line, false)
	var wg sync.WaitGroup
	line.during = func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.looks(look{}, 3)
		}()
		time.Sleep(20 * time.Millisecond)
	}

	w.Set(true)
	wg.Wait()

	if w.count() != 1 || !w.sw.Get() {
		t.Errorf("published %d times, switch says %v; want once, muted", w.count(), w.sw.Get())
	}
}

// A press that goes to a ringing alarm during the watch still leaves the microphones where they were:
// the watch has not published the hardware's move, so keep starts from the state before the press.
func TestKeepAtARingDuringTheWatch(t *testing.T) {
	line := &fakeLine{muted: true, acts: true}
	w := watch(line, true)
	prev := w.looks(look{}, 2)

	w.press(true)
	line.muted = false // a Dot's keypad driver unmutes as the button goes down
	prev = w.looks(prev, 3)
	w.keep()
	w.press(false)
	w.looks(prev, 3)

	if !line.muted || !w.sw.Get() {
		t.Errorf("line muted=%v, switch %v; want both still muted", line.muted, w.sw.Get())
	}
	if w.count() != 0 {
		t.Errorf("published %d times, want nothing", w.count())
	}
}

// A tap moves the line only where nothing else has. On amazon-gating a press of half a second or
// more is a tap here and a mute in the driver; toggling after it asked for an unmute the latch
// refused, and the switch stayed live over a set latch (#153).
func TestTapMovesTheLineOnlyWhereNothingElseHas(t *testing.T) {
	for _, tc := range []struct {
		name      string
		acts      bool
		was, line bool
		want      bool
	}{
		{"a live tap on a latch nothing else moved is the daemon's", false, false, false, true},
		{"a tap the driver already took as a mute is only news", false, false, true, false},
		{"a tap where the hardware acts is only news", true, true, false, false},
	} {
		m := &Mute{sw: &esphome.Switch{}, line: &fakeLine{muted: tc.line, acts: tc.acts}}
		if got := m.mustMove(tc.was); got != tc.want {
			t.Errorf("%s: mustMove = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// oneWay refuses every request, so that nothing past the request runs.
type oneWay struct {
	fakeLine
	asked []bool
}

func (o *oneWay) Set(muted bool) error {
	o.asked = append(o.asked, muted)
	return errors.New("refused")
}

// The screen's switch asks for the other state of the line, not for a button press: on a Show a
// press is taken as already done by the hardware, so from the screen nothing moved.
func TestFlipAsksForTheOtherState(t *testing.T) {
	for _, muted := range []bool{false, true} {
		line := &oneWay{fakeLine: fakeLine{muted: muted, acts: true}}
		m := &Mute{sw: &esphome.Switch{}, line: line}
		m.sw.Set(muted)

		m.Flip()

		if len(line.asked) != 1 || line.asked[0] != !muted {
			t.Errorf("muted=%v: asked for %v, want [%v]", muted, line.asked, !muted)
		}
	}
}
