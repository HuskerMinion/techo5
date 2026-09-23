package detect

import (
	"testing"
	"time"
)

// fakeTimer stands in for time.AfterFunc: the test fires it, so a four second duck costs no time.
type fakeTimer struct {
	fire   func()
	resets []time.Duration
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.resets = append(t.resets, d)
	return true
}

type deck struct {
	d       *ducker
	timer   *fakeTimer
	clock   time.Time
	ducked  []bool
	playing bool
	busy    bool
	on      bool
	ringing bool
	hushes  int
}

func newDeck() *deck {
	k := &deck{clock: time.Unix(1_000_000, 0), playing: true, on: true}
	k.d = &ducker{
		enabled: func() bool { return k.on },
		playing: func() bool { return k.playing },
		busy:    func() bool { return k.busy },
		duck:    func(on bool) { k.ducked = append(k.ducked, on) },
		ringing: func() bool { return k.ringing },
		hush:    func() { k.hushes++ },
		now:     func() time.Time { return k.clock },
		after: func(_ time.Duration, f func()) interface{ Reset(time.Duration) bool } {
			k.timer = &fakeTimer{fire: f}
			return k.timer
		},
	}
	return k
}

func (k *deck) tick(d time.Duration) { k.clock = k.clock.Add(d) }

func TestNearMissDucksWhileSomethingIsPlaying(t *testing.T) {
	k := newDeck()
	k.d.heard(0, 0.7)

	if len(k.ducked) != 1 || !k.ducked[0] {
		t.Fatalf("a near miss over playback should duck once, got %v", k.ducked)
	}

	// The duck ends on its own, and the level comes back.
	k.tick(nearMissDuck)
	k.timer.fire()
	if len(k.ducked) != 2 || k.ducked[1] {
		t.Fatalf("the duck should be undone when it expires, got %v", k.ducked)
	}
}

func TestNearMissDoesNothingWithNothingPlaying(t *testing.T) {
	k := newDeck()
	k.playing = false
	k.d.heard(0, 0.9)

	if len(k.ducked) != 0 {
		t.Fatalf("nothing is playing, so nothing should be ducked, got %v", k.ducked)
	}
}

func TestNearMissRespectsTheSetting(t *testing.T) {
	k := newDeck()
	k.on = false
	k.d.heard(0, 0.9)

	if len(k.ducked) != 0 {
		t.Fatalf("the setting is off, so nothing should be ducked, got %v", k.ducked)
	}
}

func TestSecondNearMissExtendsRatherThanDucksAgain(t *testing.T) {
	k := newDeck()
	k.d.heard(0, 0.7)
	k.tick(time.Second)
	k.d.heard(0, 0.6)

	if len(k.ducked) != 1 {
		t.Fatalf("a near miss inside a duck should not duck again, got %v", k.ducked)
	}
	if len(k.timer.resets) != 1 || k.timer.resets[0] != nearMissDuck {
		t.Fatalf("it should push the end out by a full duck, got %v", k.timer.resets)
	}
}

func TestADuckIsFollowedByAQuietPeriod(t *testing.T) {
	k := newDeck()
	k.d.heard(0, 0.7)
	k.tick(nearMissDuck)
	k.timer.fire()

	// Straight away: still inside the quiet period.
	k.d.heard(0, 0.7)
	if len(k.ducked) != 2 {
		t.Fatalf("a near miss inside the quiet period should not duck, got %v", k.ducked)
	}

	k.tick(nearMissQuiet)
	k.d.heard(0, 0.7)
	if len(k.ducked) != 3 || !k.ducked[2] {
		t.Fatalf("after the quiet period it should duck again, got %v", k.ducked)
	}
}

func TestAnExpiredDuckLeavesATurnAlone(t *testing.T) {
	k := newDeck()
	k.d.heard(0, 0.7)

	// The repeat was heard: a turn is running and has ducked the music itself.
	k.busy = true
	k.tick(nearMissDuck)
	k.timer.fire()

	if len(k.ducked) != 1 {
		t.Fatalf("the turn owns the level now, so this must not restore it, got %v", k.ducked)
	}
}
