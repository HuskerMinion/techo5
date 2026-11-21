//go:build spot

package display

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The Spot offers Tap on the clock as the Show does, less the deck it does not have; each choice is
// kept and read back, and nothing chosen, a value no choice has, or a Deck shared from a Show is
// Assist, as a tap on the Spot's clock always was.
func TestSpotClockTapSetting(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if got := clockTapOptions(); len(got) != 3 || got[0] != "Assist" || got[1] != "Dashboard" || got[2] != "Nothing" {
		t.Fatalf("the Spot offers %v, want Assist, Dashboard and Nothing", got)
	}
	if clockTapIndex() != 0 {
		t.Fatalf("default is %s", clockTapOptions()[clockTapIndex()])
	}
	s := clockTapSelect()
	for _, c := range []struct{ label, kept string }{{"Dashboard", "dashboard"}, {"Nothing", "nothing"}, {"Assist", ""}} {
		s.OnCommand(c.label)
		if got := config.Get().Screen.ClockTap; got != c.kept {
			t.Errorf("%s: kept %q, want %q", c.label, got, c.kept)
		}
		if got := clockTapOptions()[clockTapIndex()]; got != c.label {
			t.Errorf("%s: read back as %s", c.label, got)
		}
	}
	s.OnCommand("Deck")
	if got := config.Get().Screen.ClockTap; got != "" {
		t.Errorf("the Spot took Deck, kept %q", got)
	}
	for _, v := range []string{"deck", "sideways"} {
		if err := config.Set().Screen().ClockTap(v); err != nil {
			t.Fatal(err)
		}
		if clockTapIndex() != 0 {
			t.Errorf("%q does not read as Assist", v)
		}
	}
}

// A tap on the Spot's idle clock does what Tap on the clock says: left as Assist (or set to a Deck
// the Spot does not have) it falls through to the voice turn it has always started; Nothing takes it
// and does nothing; Dashboard puts the dashboard up, and starts a turn when there is no dashboard.
func TestSpotClockTapDoesWhatItSays(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Dashboard().Mode(config.DashboardDrawn); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		kept        string
		taken, dash bool
	}{{"", false, false}, {"deck", false, false}, {"nothing", true, false}, {"dashboard", true, true}} {
		if err := config.Set().Screen().ClockTap(c.kept); err != nil {
			t.Fatal(err)
		}
		d := &Display{poke: make(chan struct{}, 1)}
		if taken := d.clockTapTaken(); taken != c.taken || d.dash != c.dash {
			t.Errorf("%q: taken %v, dashboard %v; want %v, %v", c.kept, taken, d.dash, c.taken, c.dash)
		}
	}
	if err := config.Set().Dashboard().Mode(config.DashboardOff); err != nil {
		t.Fatal(err)
	}
	d := &Display{poke: make(chan struct{}, 1)}
	if d.clockTapTaken() || d.dash {
		t.Error("with no dashboard set up, a tap meant for one does not start a turn")
	}
}

// The Spot's Display settings have the Tap on the clock row, without the Show's line naming the deck.
func TestSpotSheetHasClockTap(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	for _, r := range categoryCard(sheetView{st: settings{cat: catDisplay}}).rows {
		if r.id == "clocktap" {
			if r.value != "Assist" || r.sub != "" {
				t.Errorf("the row reads %q under %q, want Assist and no line", r.value, r.sub)
			}
			return
		}
	}
	t.Error("no Tap on the clock row on the Spot")
}
