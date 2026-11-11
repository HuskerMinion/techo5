package timer

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
)

// A silent timer shows on the ring in its own color, and runs out without ringing: what happens when the
// wait it shows is over is Home Assistant's to say.
func TestASilentTimerShowsItsColorAndRunsOutWithoutRinging(t *testing.T) {
	ts := build()
	t.Cleanup(func() { ts.Stop() })

	green := led.Color{G: 0xFF}
	id := ts.StartSilent("joining", 2*time.Minute, green)
	if id == "" {
		t.Fatal("no timer started")
	}
	ts.mu.Lock()
	shown, now := ts.shown, ts.held[id].at
	ts.mu.Unlock()
	if len(shown) == 0 || shown[0] != green {
		t.Fatalf("the ring shows %v, want it in %v", shown, green)
	}

	ts.ripe(now.Add(2 * time.Minute))
	if ts.Ringing() {
		t.Fatal("a silent timer rang when it ran out")
	}
	if len(ts.held) != 0 {
		t.Errorf("holding %d timers, want none: a silent one is gone at zero too", len(ts.held))
	}
}

// A silent timer is not written down: it shows a wait in Home Assistant, which a restart has ended, so
// one brought back would count down to nothing. One left without a color is drawn in the usual one.
func TestASilentTimerIsNotSavedAndDefaultsToTheUsualColor(t *testing.T) {
	now := time.Now()
	held := map[string]*timer{
		"local:a": {name: "Pasta", total: time.Minute, left: time.Minute, at: now, active: true, local: true},
		"local:b": {name: "joining", total: time.Minute, left: time.Minute, at: now, active: true, local: true, silent: true},
	}
	if got := localList(held, now); len(got) != 1 || got[0].ID != "local:a" {
		t.Fatalf("saved %+v, want only the timer that rings", got)
	}

	ts := build()
	t.Cleanup(func() { ts.Stop() })
	id := ts.StartSilent("joining", time.Minute, led.Color{})
	t.Cleanup(func() { ts.Cancel(id) })
	ts.mu.Lock()
	shown := ts.shown
	ts.mu.Unlock()
	if len(shown) == 0 || shown[0] != countdownColor {
		t.Fatalf("the ring shows %v, want %v", shown, countdownColor)
	}
}

func TestParseColor(t *testing.T) {
	for in, want := range map[string]led.Color{
		"":              {},
		"#00ff80":       {G: 0xFF, B: 0x80},
		"00FF80":        {G: 0xFF, B: 0x80},
		" #f80 ":        {R: 0xFF, G: 0x88},
		"255, 140, 0":   {R: 255, G: 140},
		"[0, 128, 255]": {G: 128, B: 255},
	} {
		got, err := parseColor(in)
		if err != nil || got != want {
			t.Errorf("parseColor(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"green", "#12345", "#1234567", "256, 0, 0", "1, 2"} {
		if _, err := parseColor(in); err == nil {
			t.Errorf("parseColor(%q) took it as a color", in)
		}
	}
}
