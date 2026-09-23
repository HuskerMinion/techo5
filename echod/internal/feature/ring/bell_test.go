package ring

import (
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// room records what the bell did to the rest of the device.
type room struct {
	mu      sync.Mutex
	attends []bool
	chimes  int
}

func (r *room) took() []bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]bool(nil), r.attends...)
}

func (r *room) rounds() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.chimes
}

// quietBell runs the bell fast, into a recorder instead of a speaker and a light.
func quietBell(t *testing.T, rings time.Duration) *room {
	t.Helper()
	rm := &room{}
	wasFor, wasEvery, wasChime, wasAttend := ringFor, ringEvery, chime, attend
	ringFor, ringEvery = rings, 5*time.Millisecond
	chime = func([]speaker.Note) { rm.mu.Lock(); rm.chimes++; rm.mu.Unlock() }
	attend = func(on bool) { rm.mu.Lock(); rm.attends = append(rm.attends, on); rm.mu.Unlock() }
	t.Cleanup(func() {
		waitFor(t, "the bell to stop", func() bool {
			bell.mu.Lock()
			defer bell.mu.Unlock()
			return !bell.running
		})
		ringFor, ringEvery, chime, attend = wasFor, wasEvery, wasChime, wasAttend
		state.mu.Lock()
		state.sounding, state.until, state.offer = 0, time.Time{}, time.Time{}
		state.mu.Unlock()
	})
	return rm
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// ends counts how many times a ring's ended was called.
type ends struct {
	mu sync.Mutex
	n  int
}

func (e *ends) ended() { e.mu.Lock(); e.n++; e.mu.Unlock() }

func (e *ends) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.n
}

func TestAStoppedRingEndsOnceAndGivesTheRoomBack(t *testing.T) {
	rm := quietBell(t, time.Minute)
	var e ends

	stop := Start("alarm", nil, e.ended)
	// Sounding before Start returns, so nothing sees a ring started and silent.
	if !IsSounding() {
		t.Fatal("not sounding straight after Start")
	}
	waitFor(t, "a chime", func() bool { return rm.rounds() > 0 })

	stop()
	stop() // a second stop, from a second button, is nothing
	waitFor(t, "ended", func() bool { return e.count() > 0 })
	waitFor(t, "the bell to stop", func() bool {
		bell.mu.Lock()
		defer bell.mu.Unlock()
		return !bell.running
	})
	if e.count() != 1 {
		t.Errorf("ended called %d times, want 1", e.count())
	}
	if IsSounding() {
		t.Error("still sounding after the only ring ended")
	}
	if got := rm.took(); len(got) != 2 || !got[0] || got[1] {
		t.Errorf("light and duck went %v, want taken once and given back once", got)
	}
}

// The reason there is one bell: the music comes back up when the last ring ends, not the first.
func TestTwoRingsShareOneLightAndOneDuck(t *testing.T) {
	rm := quietBell(t, time.Minute)
	var alarm, timer ends

	stopAlarm := Start("alarm", nil, alarm.ended)
	stopTimer := Start("timer", nil, timer.ended)

	stopTimer()
	waitFor(t, "the timer to end", func() bool { return timer.count() == 1 })
	if got := rm.took(); len(got) != 1 || !got[0] {
		t.Fatalf("with the alarm still ringing, light and duck went %v, want only taken", got)
	}
	if !IsSounding() {
		t.Fatal("the alarm stopped sounding when the timer ended")
	}

	stopAlarm()
	waitFor(t, "the alarm to end", func() bool { return alarm.count() == 1 })
	waitFor(t, "the room back", func() bool { return len(rm.took()) == 2 })
	if got := rm.took(); got[1] {
		t.Errorf("light and duck went %v, want given back at the end", got)
	}
}

func TestARingNobodyStopsRingsOut(t *testing.T) {
	quietBell(t, 30*time.Millisecond)
	var e ends

	Start("timer", nil, e.ended)
	waitFor(t, "the ring to run out", func() bool { return e.count() == 1 })
	if IsSounding() {
		t.Error("still sounding after ringing out")
	}
}

// A button press silences, and nobody answers the offer: every ring on the bell ends, not just one.
func TestAnUnansweredSilenceEndsEveryRing(t *testing.T) {
	rm := quietBell(t, time.Minute)
	now := time.Now()
	was := state.now
	var mu sync.Mutex
	state.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	t.Cleanup(func() { state.now = was })

	var alarm, timer ends
	Start("alarm", nil, alarm.ended)
	Start("timer", nil, timer.ended)
	waitFor(t, "a chime", func() bool { return rm.rounds() > 0 })

	if !Silence() {
		t.Fatal("nothing to silence")
	}
	quiet := rm.rounds()
	time.Sleep(4 * ringEvery)
	if rm.rounds() != quiet {
		t.Error("a silenced ring went on chiming")
	}

	mu.Lock()
	now = now.Add(OfferFor)
	mu.Unlock()
	nudge()
	waitFor(t, "both rings to end", func() bool { return alarm.count() == 1 && timer.count() == 1 })
}
