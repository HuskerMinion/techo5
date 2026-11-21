//go:build panel && !dot

package display

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
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

// On a panel, Tap on the clock offers Apps; a tap set to it opens the settings with the apps list, the
// PIN asked first when the lock is on, and with no list from the app it does nothing of its own.
func TestClockTapOpensTheAppsList(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	told := tabletAppsFor(t)
	if !slices.Contains(clockTapOptions(), "Apps") {
		t.Fatalf("a panel's Tap on the clock: %q, want Apps among them", clockTapOptions())
	}
	d := &Display{poke: make(chan struct{}, 1)}
	if d.openTabletApps() {
		t.Fatal("no list from the app, and the clock's tap still opened one")
	}
	appSaysApps([]string{"begin"})
	appSaysApps([]string{"item", "org.example.notes", "Notes"})
	appSaysApps([]string{"end"})
	if !d.openTabletApps() || !d.sheetUp() || d.picker != "tabletapps" {
		t.Fatalf("unlocked: settings up %v, picker %q", d.sheetUp(), d.picker)
	}
	if !slices.Equal(*told, []string{"apps list"}) {
		t.Errorf("the app was told %q", *told)
	}

	if err := security.Get().SetPIN("2468"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pinPress("cancel")
		_ = security.Get().SetPIN("")
	})
	d.showSheet(false)
	if !d.openTabletApps() || d.picker != "" || !pinIsOpen() {
		t.Fatalf("locked: picker %q, pad up %v; want the pad first", d.picker, pinIsOpen())
	}
	typePIN("2468")
	if d.picker != "tabletapps" {
		t.Errorf("after the PIN: picker %q", d.picker)
	}
}
