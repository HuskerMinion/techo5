//go:build !dot

package dashboard

import (
	"slices"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A name and a path each, the way Home Assistant answers: these are the picker's own.
var pickerBoards = []config.DashboardChoice{
	{Label: "Overview", Path: "lovelace"},
	{Label: "Kitchen · Lights", Path: "dashboard-kitchen/lights"},
}

// The picker has dashboards to offer with the dashboard off, which is the state somebody choosing one
// is in, and the only state a device that has never shown a dashboard is in.
func TestThePickerIsPopulatedWithTheDashboardOff(t *testing.T) {
	f := &Feature{board: &esphome.Select{}}
	f.listBoards(config.Dashboard{Known: pickerBoards})

	want := []string{automatic, "Overview", "Kitchen · Lights"}
	if !slices.Equal(f.board.Options, want) {
		t.Errorf("options = %q, want %q", f.board.Options, want)
	}
	if got := f.board.Get(); got != automatic {
		t.Errorf("settled on %q with nothing chosen, want %q", got, automatic)
	}
}

// A chosen path Home Assistant no longer lists stays a choice of its own, and is still what the picker
// says it is showing: the list never claims a different dashboard than the one on the screen.
func TestThePickerKeepsAChosenDashboardHomeAssistantNoLongerLists(t *testing.T) {
	f := &Feature{board: &esphome.Select{}}
	f.listBoards(config.Dashboard{Path: "dashboard-kitchen/lights", Known: pickerBoards[:1]})

	want := []string{automatic, "Overview", "dashboard-kitchen/lights"}
	if !slices.Equal(f.board.Options, want) {
		t.Errorf("options = %q, want %q", f.board.Options, want)
	}
	if got := f.board.Get(); got != "dashboard-kitchen/lights" {
		t.Errorf("settled on %q, want the chosen path", got)
	}
}

// The list is asked for while the dashboard is off only while there is nothing cached: once a device
// has been given a list it stops asking, which is the whole of "no dashboard polling while it is off".
func TestTheListIsAskedForWhileThereIsNothingCached(t *testing.T) {
	for _, c := range []struct {
		what  string
		mode  config.DashboardMode
		known []config.DashboardChoice
		want  bool
	}{
		{"off with nothing cached, the state a fresh device is in", config.DashboardOff, nil, true},
		{"off with a list", config.DashboardOff, pickerBoards, false},
		{"drawn with nothing cached", config.DashboardDrawn, nil, true},
		{"drawn with a list", config.DashboardDrawn, pickerBoards, true},
		{"streamed with a list", config.DashboardStreamed, pickerBoards, true},
	} {
		if got := wantsList(c.mode, c.known); got != c.want {
			t.Errorf("%s: wantsList = %v, want %v", c.what, got, c.want)
		}
	}
}
