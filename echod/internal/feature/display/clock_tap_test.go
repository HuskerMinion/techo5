//go:build !dot && !spot

package display

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// Each Tap on the clock choice is kept and read back; nothing chosen, or a value no choice has, is
// Assist, as a tap always was.
func TestClockTapSetting(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if clockTapIndex() != 0 {
		t.Fatalf("default is %s", clockTaps[clockTapIndex()].label)
	}
	s := clockTapSelect()
	for _, c := range []struct{ label, kept string }{{"Dashboard", "dashboard"}, {"Nothing", "nothing"}, {"Deck", "deck"}, {"Assist", ""}} {
		s.OnCommand(c.label)
		if got := config.Get().Screen.ClockTap; got != c.kept {
			t.Errorf("%s: kept %q, want %q", c.label, got, c.kept)
		}
		if got := clockTaps[clockTapIndex()].label; got != c.label {
			t.Errorf("%s: read back as %s", c.label, got)
		}
	}
	if err := config.Set().Screen().ClockTap("sideways"); err != nil {
		t.Fatal(err)
	}
	if clockTapIndex() != 0 {
		t.Error("an unknown value is not Assist")
	}
	s.OnCommand("Assist")
}

// A dashboard opened by hand comes back to the clock after the Dashboard returns setting, and stays
// with Never.
func TestDashboardReturnsAfterTheSetting(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	f := dashboard.Get()
	t.Cleanup(func() { f.SetBack("10 minutes") }) // the tests after this one expect the default
	check := func(choice string, untouched time.Duration, want bool) {
		t.Helper()
		f.SetBack(choice)
		d := &Display{}
		d.dash, d.dashTouched = true, time.Now().Add(-untouched)
		d.dashScene(&scene{phase: "idle"}, false)
		if d.dash == want {
			t.Errorf("%s, untouched %v: still up %v", choice, untouched, d.dash)
		}
	}
	check("1 minute", 90*time.Second, true)
	check("1 minute", 30*time.Second, false)
	check("10 minutes", 9*time.Minute, false)
	check("10 minutes", 11*time.Minute, true)
	check("Never", 24*time.Hour, false)
}
