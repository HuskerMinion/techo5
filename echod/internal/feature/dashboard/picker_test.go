//go:build !dot

package dashboard

import (
	"fmt"
	"slices"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
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
	f := pickerFeature()
	f.listBoards(config.Dashboard{Known: pickerBoards})

	want := []string{automatic, "Overview", "Kitchen · Lights"}
	if got := f.boardOpts.Get(); !slices.Equal(got, want) {
		t.Errorf("options = %q, want %q", got, want)
	}
	if got := f.board.Get(); got != automatic {
		t.Errorf("settled on %q with nothing chosen, want %q", got, automatic)
	}
}

// A chosen path Home Assistant no longer lists stays a choice of its own, and is still what the picker
// says it is showing: the list never claims a different dashboard than the one on the screen.
func TestThePickerKeepsAChosenDashboardHomeAssistantNoLongerLists(t *testing.T) {
	f := pickerFeature()
	f.listBoards(config.Dashboard{Path: "dashboard-kitchen/lights", Known: pickerBoards[:1]})

	want := []string{automatic, "Overview", "dashboard-kitchen/lights"}
	if got := f.boardOpts.Get(); !slices.Equal(got, want) {
		t.Errorf("options = %q, want %q", got, want)
	}
	if got := f.board.Get(); got != "dashboard-kitchen/lights" {
		t.Errorf("settled on %q, want the chosen path", got)
	}
}

// A path Home Assistant does not list, chosen by dashboard_path or a navigate tap, is a new option,
// and Home Assistant only hears of it on its next connection: the reconnect is asked for, or its
// select shows unknown until then. The same list again asks for nothing.
func TestANewPathAsksForTheReconnect(t *testing.T) {
	f := pickerFeature()
	f.listBoards(config.Dashboard{Known: pickerBoards})
	f.relist = false
	f.listBoards(config.Dashboard{Known: pickerBoards})
	if f.relist {
		t.Errorf("the same list asked for a reconnect")
	}
	f.listBoards(config.Dashboard{Path: "dashboard-garage/0", Known: pickerBoards})
	if !f.relist {
		t.Errorf("a new path in the list asked for no reconnect")
	}
}

// A view reached by another name than the list's is the list's option, so going to it adds none and
// costs no reconnect: a one-view dashboard by any view, a view by its number, a dashboard of several
// views by its path alone.
func TestAViewByAnotherNameIsTheListedOne(t *testing.T) {
	known := []config.DashboardChoice{
		{Label: "Overview", Path: "lovelace"},
		{Label: "Kitchen · Lights", Path: "dashboard-kitchen/lights"},
		{Label: "Kitchen · View 2", Path: "dashboard-kitchen/1"},
		{Label: "Kitchen · Heat", Path: "dashboard-kitchen/heat"},
		{Label: "Energy (streamed only)", Path: "energy", Streamed: true},
	}
	for _, c := range []struct{ path, want string }{
		{"", ""},
		{"lovelace", "lovelace"},
		{"lovelace/0", "lovelace"},
		{"/lovelace/home/", "lovelace"},
		{"dashboard-kitchen/0", "dashboard-kitchen/lights"},
		{"dashboard-kitchen/1", "dashboard-kitchen/1"},
		{"dashboard-kitchen/2", "dashboard-kitchen/heat"},
		{"dashboard-kitchen", "dashboard-kitchen/lights"},
		{"dashboard-kitchen/9", "dashboard-kitchen/9"},
		{"dashboard-kitchen/porch", "dashboard-kitchen/porch"},
		{"dashboard-garage/0", "dashboard-garage/0"},
		{"/dashboard-kitchen/heat?edit=1", "dashboard-kitchen/heat"},
		{"dashboard-kitchen/heat#popup", "dashboard-kitchen/heat"},
		{"lovelace/0?x=1#y", "lovelace"},
	} {
		if got := listedPath(c.path, known); got != c.want {
			t.Errorf("listedPath(%q) = %q, want %q", c.path, got, c.want)
		}
	}

	f := pickerFeature()
	f.listBoards(config.Dashboard{Known: known})
	f.relist = false
	for _, p := range []string{"lovelace/0", "dashboard-kitchen/0", "dashboard-kitchen/2", "dashboard-kitchen"} {
		f.listBoards(config.Dashboard{Path: p, Known: known})
		if f.relist {
			t.Errorf("going to %q changed the list: %q", p, f.boardOpts.Get())
		}
	}
	if got := f.board.Get(); got != "Kitchen · Lights" {
		t.Errorf("the kitchen dashboard by its path alone shows as %q, want its first view", got)
	}
}

// A navigation path's query and hash are not part of the path kept.
func TestANavigationPathIsKeptWithoutItsQuery(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"/dash/lights?x=1", "dash/lights"},
		{"dash/lights#popup", "dash/lights"},
		{" /dash/?a=b#c ", "dash"},
		{"dash/lights", "dash/lights"},
	} {
		if got := navPath(c.in); got != c.want {
			t.Errorf("navPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A changed list's reconnect waits for the device to be out of a turn, and comes at most once in
// relistGap however often the list changes; one that has to wait is not lost.
func TestRelistReconnectsAreSpacedOut(t *testing.T) {
	f := pickerFeature()
	now := time.Now()
	if f.relistDue(now, false) {
		t.Fatal("a reconnect with no changed list")
	}
	f.relist = true
	if f.relistDue(now, true) {
		t.Fatal("a reconnect in the middle of a turn")
	}
	if !f.relistDue(now, false) {
		t.Fatal("no reconnect for a changed list")
	}
	f.relist = true
	if f.relistDue(now.Add(relistGap/2), false) {
		t.Fatal("a second reconnect within relistGap")
	}
	if !f.relistDue(now.Add(relistGap), false) {
		t.Fatal("the pending reconnect was lost")
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

// A dashboard made after the list filled up is still in it (#743). Home Assistant lists a new
// dashboard last, and every view is an option, so a few dashboards of many views used to fill the list
// and cut each new dashboard, however long one waited. Each dashboard now has a place, its first
// view, before more views take the room left; the chosen view stays whatever is cut.
func TestANewDashboardHasAPlaceInAFullList(t *testing.T) {
	var known []config.DashboardChoice
	for d := range 4 {
		for v := range 30 {
			known = append(known, config.DashboardChoice{
				Label: fmt.Sprintf("Big %d · View %d", d, v),
				Path:  fmt.Sprintf("dashboard-big%d/%d", d, v),
			})
		}
	}
	known = append(known, config.DashboardChoice{Label: "New", Path: "dashboard-new"},
		config.DashboardChoice{Label: "Energy (streamed only)", Path: "energy", Streamed: true})

	opts, chosen := boardOptions(config.Dashboard{Known: known, Path: "dashboard-big3/29"})
	if len(opts) > boardsMax {
		t.Errorf("%d options, more than the cap of %d", len(opts), boardsMax)
	}
	for _, want := range []string{"New", "Energy (streamed only)", "Big 1 · View 0", "Big 3 · View 0", "Big 3 · View 29"} {
		if !slices.Contains(opts, want) {
			t.Errorf("%q left out of a full list", want)
		}
	}
	if chosen != "Big 3 · View 29" {
		t.Errorf("chosen = %q, want the chosen view", chosen)
	}
	// Kept in Home Assistant's order: the views that fit, then the new one, then the built-in page.
	if i, j := slices.Index(opts, "New"), slices.Index(opts, "Energy (streamed only)"); i > j || opts[1] != "Big 0 · View 0" {
		t.Errorf("options out of order: %q", opts)
	}

	// A list with room for all of it is all of it.
	opts, _ = boardOptions(config.Dashboard{Known: known[100:]})
	if len(opts) != len(known[100:])+1 {
		t.Errorf("a list with room left out %d", len(known[100:])+1-len(opts))
	}
}

// pickerFeature is a Feature with just the picker.
func pickerFeature() *Feature {
	f := &Feature{board: &esphome.Select{}}
	f.boardOpts = component.NewOptions(f.board)
	return f
}
