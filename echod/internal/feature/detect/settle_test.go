package detect

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// While settling, a score over the threshold wakes nothing; once the time is up, the same score
// wakes. A longer hold already running is not cut short.
func TestSettlingHoldsDetectionsBack(t *testing.T) {
	fakes(t)
	e := New(2, nil)
	e.Threshold = func(int) float64 { return 0.7 }
	if err := e.Use(0, model("hey_jarvis", wake.KindMicroWakeWord)); err != nil {
		t.Fatalf("Use(0): %v", err)
	}
	woke := make(chan int, 4)
	e.OnDetect = func(slot int) { woke <- slot }

	e.Settle(time.Second)
	s := &e.slots[0]
	at := time.Now()
	e.judge(0, s, 0.95, at, nil)
	select {
	case <-woke:
		t.Fatal("woke while settling")
	case <-time.After(50 * time.Millisecond):
	}

	e.Settle(time.Millisecond) // shorter than the hold already running
	e.judge(0, s, 0.95, at.Add(500*time.Millisecond), nil)
	select {
	case <-woke:
		t.Fatal("a shorter settle cut the running one short")
	case <-time.After(50 * time.Millisecond):
	}

	e.judge(0, s, 0.95, at.Add(1100*time.Millisecond), nil)
	select {
	case <-woke:
	case <-time.After(time.Second):
		t.Fatal("nothing woke once settling was over")
	}
}

// The hold after an unmute lasts as long as the unmute sound, whichever one plays, and a little
// after.
func TestUnmuteSettlesForTheWholeSound(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	for _, classic := range []bool{false, true} {
		if err := config.Set().Speaker().ClassicSounds(classic); err != nil {
			t.Fatal(err)
		}
		var sound time.Duration
		for _, n := range speaker.MuteSound(false) {
			sound += time.Duration(n.Ms) * time.Millisecond
		}
		if got := unmuteSettle(); got < sound+settleTail || sound == 0 {
			t.Errorf("classic=%v: settles %v for a %v sound", classic, got, sound)
		}
	}
}
