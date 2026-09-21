package timezone

import (
	"os"
	"path/filepath"
	"testing"
)

// The lists the screen offers come off the zone database in the image: regions to choose from, and
// the zones inside one. A device without the database offers nothing rather than failing.
func TestRegionsAndZonesComeFromTheDatabase(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"America/Denver", "America/Indiana/Knox", "Europe/London", "posix/America/Denver"} {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "zone.tab"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := zoneinfo
	zoneinfo = dir
	t.Cleanup(func() { zoneinfo = old })

	regions := Regions()
	want := map[string]bool{"America": true, "Europe": true}
	for _, r := range regions {
		if !want[r] {
			t.Errorf("region %q offered; posix, right and the loose files are not zones anyone picks", r)
		}
		delete(want, r)
	}
	for r := range want {
		t.Errorf("region %q missing", r)
	}

	zones := Zones("America")
	if len(zones) != 2 || zones[0] != "Denver" || zones[1] != "Indiana/Knox" {
		t.Errorf("America has %v, want Denver and Indiana/Knox: a zone inside a region's own folder still counts", zones)
	}
	if got := Zones("../etc"); got != nil {
		t.Errorf("Zones(%q) = %v, want nothing: a region name cannot walk out of the database", "../etc", got)
	}
}

// A zone chosen on the device is remembered as chosen here, so Home Assistant's own zone no longer
// replaces it; following Home Assistant again undoes that.
func TestChoosingHereHoldsUntilItIsGivenBack(t *testing.T) {
	dir := t.TempDir()
	oldName, oldLink, oldHere := nameFile, linkFile, hereFile
	nameFile, linkFile, hereFile = dir+"/timezone", dir+"/localtime", dir+"/timezone-set-here"
	t.Cleanup(func() { nameFile, linkFile, hereFile = oldName, oldLink, oldHere })

	z := Get()
	if z.SetHere() {
		t.Fatal("a device that has chosen nothing says the zone was chosen on it")
	}
	if err := z.Choose("MST7MDT,M3.2.0,M11.1.0"); err != nil {
		t.Fatalf("choosing a zone: %v", err)
	}
	if !z.SetHere() {
		t.Error("a zone chosen here is not remembered as chosen here, so Home Assistant would take it back")
	}
	if err := z.Follow(); err != nil {
		t.Fatalf("following again: %v", err)
	}
	if z.SetHere() {
		t.Error("still says the zone was chosen here after being given back to Home Assistant")
	}
}
