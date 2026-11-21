//go:build panel && !dot

package display

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// tabletAppsFor puts the list back after a test, and keeps the screen and the app out of it.
func tabletAppsFor(t *testing.T) *[]string {
	t.Helper()
	told := androidSettingsFor(t)
	tabletApps.Lock()
	apps, reading, open := tabletApps.apps, tabletApps.reading, tabletApps.open
	tabletApps.apps, tabletApps.reading, tabletApps.open = nil, nil, false
	tabletApps.Unlock()
	t.Cleanup(func() {
		tabletApps.Lock()
		tabletApps.apps, tabletApps.reading, tabletApps.open = apps, reading, open
		tabletApps.Unlock()
	})
	return told
}

// An app from before the lines sends no list: no Apps row. A whole list shows it, labels with spaces
// kept; an item outside begin and end, or a list never ended, changes nothing.
func TestTabletAppsRowNeedsAWholeList(t *testing.T) {
	tabletAppsFor(t)
	if r, ok := generalRow(t, "tabletapps"); ok {
		t.Fatalf("no list from the app: row %+v", r)
	}
	appSaysApps([]string{"item", "org.example.stray", "Stray"})
	appSaysApps([]string{"begin"})
	appSaysApps([]string{"item", "org.example.half", "Half"})
	if _, ok := generalRow(t, "tabletapps"); ok {
		t.Fatal("a list not yet ended showed the row")
	}
	appSaysApps([]string{"begin"})
	appSaysApps([]string{"item", "org.example.notes", "Notes"})
	appSaysApps([]string{"item", "org.example.cam", "Camera", "Viewer"})
	appSaysApps([]string{"item", "org.example.nolabel"})
	appSaysApps([]string{"end"})
	r, ok := generalRow(t, "tabletapps")
	if !ok {
		t.Fatal("no Apps row after a whole list")
	}
	if r.kind != ctlButton || r.label != "Apps" || r.button != "Open" || r.sub != "Open another app on this tablet" {
		t.Errorf("row %+v", r)
	}
	p, ok := tabletAppsPicker()
	if !ok || !slices.Equal(p.opts, []string{"Notes", "Camera Viewer"}) || p.cur != -1 {
		t.Errorf("picker %+v", p)
	}
	appSaysApps([]string{"begin"})
	appSaysApps([]string{"end"})
	if _, ok := generalRow(t, "tabletapps"); ok {
		t.Error("an empty list still showed the row")
	}
}

// Open asks the app for the list again and opens it; a pick asks the app to open that app, and a pick
// past the end asks for nothing.
func TestTabletAppsOpenAndPick(t *testing.T) {
	told := tabletAppsFor(t)
	appSaysApps([]string{"begin"})
	appSaysApps([]string{"item", "org.example.notes", "Notes"})
	appSaysApps([]string{"item", "org.example.cam", "Camera"})
	appSaysApps([]string{"end"})
	d := &Display{poke: make(chan struct{}, 1)}
	d.rowTap("tabletapps", partMain, 0)
	if d.picker != "tabletapps" {
		t.Errorf("picker %q; want the apps list open", d.picker)
	}
	d.choose("tabletapps", 1)
	d.choose("tabletapps", 2)
	if want := []string{"apps list", "apps open org.example.cam"}; !slices.Equal(*told, want) {
		t.Errorf("the app was told %q; want %q", *told, want)
	}
}

// On a panel, Tap on the clock offers Apps; a tap set to it puts the apps list up over the clock, not
// the settings, the PIN asked first when the lock is on; with no list from the app it does nothing of its
// own; and any finger that is not a pick puts the list away.
func TestClockTapOpensTheAppsList(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	told := tabletAppsFor(t)
	if !slices.Contains(clockTapOptions(), "Apps") {
		t.Fatalf("a panel's Tap on the clock: %q, want Apps among them", clockTapOptions())
	}
	d := &Display{poke: make(chan struct{}, 1)}
	popped := func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.appsPop
	}
	if d.openTabletApps() || popped() {
		t.Fatal("no list from the app, and the clock's tap still opened one")
	}
	appSaysApps([]string{"begin"})
	appSaysApps([]string{"item", "org.example.notes", "Notes"})
	appSaysApps([]string{"end"})
	if !d.openTabletApps() || !popped() || d.sheetUp() {
		t.Fatalf("unlocked: list up %v, settings up %v; want the list alone", popped(), d.sheetUp())
	}
	if !slices.Equal(*told, []string{"apps list"}) {
		t.Errorf("the app was told %q", *told)
	}
	d.appsPopGesture(touch.Gesture{Kind: touch.SwipeLeft})
	if popped() {
		t.Error("a swipe across left the list up")
	}

	if err := security.Get().SetPIN("2468"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pinPress("cancel")
		_ = security.Get().SetPIN("")
	})
	if !d.openTabletApps() || popped() || !pinIsOpen() {
		t.Fatalf("locked: list up %v, pad up %v; want the pad first", popped(), pinIsOpen())
	}
	typePIN("2468")
	if !popped() || d.sheetUp() {
		t.Errorf("after the PIN: list up %v, settings up %v", popped(), d.sheetUp())
	}
}

// The PIN typed for the clock's apps list opens it only if the clock is still what is showing: with the
// settings come up over it meanwhile, the right PIN opens nothing and the lock goes back on.
func TestTheAppsListPINOpensNothingUnderTheSettings(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	tabletAppsFor(t)
	appSaysApps([]string{"begin"})
	appSaysApps([]string{"item", "org.example.notes", "Notes"})
	appSaysApps([]string{"end"})
	if err := security.Get().SetPIN("2468"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pinPress("cancel")
		_ = security.Get().SetPIN("")
	})
	d := &Display{poke: make(chan struct{}, 1)}
	if !d.openTabletApps() || !pinIsOpen() {
		t.Fatal("no pad for the locked list")
	}
	d.showSheet(true)
	typePIN("2468")
	d.mu.Lock()
	popped := d.appsPop
	d.mu.Unlock()
	if popped || !security.Locked() {
		t.Errorf("under the settings: list up %v, locked %v; want neither open", popped, security.Locked())
	}
}
