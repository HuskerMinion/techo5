//go:build !panel

package timezone

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

// A zone from Home Assistant lands on userdata as a name and a link into zoneinfo, and asks for the
// restart that puts it in force; one this image does not have changes nothing.
func TestApply(t *testing.T) {
	saved := time.Local.String()
	if _, err := time.LoadLocation("Europe/Berlin"); err != nil {
		t.Skip("no zoneinfo on this machine:", err)
	}
	system := "/usr/share/zoneinfo"
	if _, err := os.Stat(filepath.Join(system, "Europe/Berlin")); err != nil {
		t.Skip("no zoneinfo directory:", err)
	}
	dir := t.TempDir()
	nameFile, linkFile, zoneinfo = filepath.Join(dir, "timezone"), filepath.Join(dir, "localtime"), system
	drainRestarts()

	z := &Zone{}
	if err := z.apply("Europe/Berlin"); err != nil {
		t.Fatal(err)
	}
	if got := z.Current(); got != "Europe/Berlin" {
		t.Errorf("saved zone = %q", got)
	}
	if target, err := os.Readlink(linkFile); err != nil || target != filepath.Join(system, "Europe/Berlin") {
		t.Errorf("link = %q, %v", target, err)
	}
	// The new zone is in force once the process is a new one, so what a successful apply leaves behind
	// is a restart request. time.Local is not touched: reassigning it races every goroutine reading it.
	if why := restartAsked(); why != "time zone Europe/Berlin" {
		t.Errorf("restart asked for %q", why)
	}
	if time.Local.String() != saved {
		t.Errorf("time.Local was changed to %s", time.Local)
	}

	if err := z.apply("Nowhere/Atlantis!"); err == nil {
		t.Error("an unknown zone was applied")
	}
	if got := z.Current(); got != "Europe/Berlin" {
		t.Errorf("an unknown zone replaced the saved one: %q", got)
	}
	if err := z.apply(""); err != nil || z.Current() != "Europe/Berlin" {
		t.Errorf("an empty zone changed something: %v %q", err, z.Current())
	}
}
