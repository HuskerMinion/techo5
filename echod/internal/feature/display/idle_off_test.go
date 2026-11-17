//go:build !dot

package display

import (
	"path/filepath"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
)

// idleOffDisplay is a lit screen with a fresh config and Screen off after idle at minutes, presence
// detection off, and the room's state as at start.
func idleOffDisplay(t *testing.T, minutes int) *Display {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Screen().OffAfterMinutes(minutes); err != nil {
		t.Fatal(err)
	}
	awayDark.mu.Lock()
	awayDark.dark, awayDark.idle, awayDark.wasOn = false, false, false
	awayDark.litAt, awayDark.usedAt = time.Time{}, time.Time{}
	awayDark.mu.Unlock()
	return &Display{
		poke: make(chan struct{}, 1),
		view: voice.State{Phase: "idle"},
		light: &esphome.Light{
			Base:                esphome.Base{ObjectID: "screen", Name: "Screen", Icon: "mdi:monitor"},
			SupportedColorModes: []esphome.ColorMode{esphome.ColorModeBrightness},
		},
		on: true, ceiling: 60,
	}
}

// Screen off after idle puts the screen out once it has gone the wait without a touch or a turn, not
// before, not at night and not while something is going on, with no camera; a touch or the wake word
// lights it again, and it gets its full wait from then.
func TestIdleOffWaitsForTouchesAndTurns(t *testing.T) {
	d := idleOffDisplay(t, 5)
	start := time.Now()
	tick := func(at time.Duration, busy, spoken, night bool) {
		t.Helper()
		d.mu.Lock()
		on := d.on
		d.mu.Unlock()
		d.awayTick(start.Add(at), on, busy, spoken, night)
	}
	lit := func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.on
	}

	tick(0, false, false, false) // the first look starts the wait
	d.mu.Lock()
	d.touchedAt = start.Add(3 * time.Minute) // a finger on it: Home Assistant would not have seen this
	d.mu.Unlock()
	tick(7*time.Minute+59*time.Second, false, false, false)
	if !lit() {
		t.Fatal("out less than the wait after a touch")
	}
	tick(8*time.Minute, false, false, true)
	if !lit() {
		t.Fatal("out at night, which has its own rules")
	}
	tick(8*time.Minute, true, false, false) // something going on: the wait starts again when it ends
	tick(12*time.Minute+59*time.Second, false, false, false)
	if !lit() {
		t.Fatal("out less than the wait after something went on")
	}
	tick(13*time.Minute, false, false, false)
	if lit() {
		t.Fatal("still lit after the wait")
	}
	tick(20*time.Minute, false, false, false)
	if lit() {
		t.Fatal("lit again with nobody touching it or speaking")
	}

	tick(21*time.Minute, true, true, true)
	if lit() {
		t.Fatal("the wake word lit it at night")
	}
	tick(22*time.Minute, true, true, false)
	if !lit() {
		t.Fatal("the wake word left it out")
	}
	tick(25*time.Minute, true, true, false) // the turn goes on to here
	tick(29*time.Minute+59*time.Second, false, false, false)
	if !lit() {
		t.Fatal("out less than the wait after the turn")
	}
	tick(30*time.Minute, false, false, false)
	if lit() {
		t.Fatal("still lit the wait after the turn")
	}

	// A tap on the dark screen, as the touch handler lights it.
	d.mu.Lock()
	d.touchedAt = start.Add(31 * time.Minute)
	d.mu.Unlock()
	d.apply(true, d.ceilingOrDefault(), true)
	tick(31*time.Minute, false, false, false)
	tick(35*time.Minute+59*time.Second, false, false, false)
	if !lit() {
		t.Fatal("out less than the wait after the tap that lit it")
	}
	tick(36*time.Minute, false, false, false)
	if lit() {
		t.Fatal("still lit the wait after the tap")
	}

	// Turned to never while it is out: nothing keeps it out any more.
	if err := config.Set().Screen().OffAfterMinutes(0); err != nil {
		t.Fatal(err)
	}
	tick(37*time.Minute, false, false, false)
	if !lit() {
		t.Fatal("left out with Screen off after idle at never")
	}
	tick(2*time.Hour, false, false, false)
	if !lit() {
		t.Fatal("out with Screen off after idle at never")
	}
}

// A screen Home Assistant turned off stays off: going unused only puts out what it lit, and the wake
// word only lights what it put out.
func TestIdleOffLeavesAScreenTurnedOffAlone(t *testing.T) {
	d := idleOffDisplay(t, 1)
	start := time.Now()
	d.awayTick(start, true, false, false, false)
	d.apply(false, d.ceilingOrDefault(), true) // Home Assistant
	d.awayTick(start.Add(time.Minute), false, true, true, false)
	d.mu.Lock()
	on := d.on
	d.mu.Unlock()
	if on {
		t.Fatal("the wake word lit a screen Home Assistant turned off")
	}
}

// The setting's names in Home Assistant are the ones automations set it by.
func TestIdleOffNames(t *testing.T) {
	want := []string{"Never", "1 minute", "2 minutes", "5 minutes", "10 minutes", "15 minutes", "30 minutes", "1 hour"}
	got := idleOffNames()
	if len(got) != len(want) {
		t.Fatalf("names %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("names %q, want %q", got, want)
		}
	}
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Screen().OffAfterMinutes(7); err != nil {
		t.Fatal(err)
	}
	if idleOffAfter() != 0 {
		t.Fatal("a wait no choice has is not never")
	}
}
