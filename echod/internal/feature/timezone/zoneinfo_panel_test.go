//go:build panel

package timezone

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Android's packed database is read by name: the lists the screen offers come off its index, and a
// named zone lands on userdata as its own zoneinfo data, since there is no directory to link into.
func TestAndroidZoneDatabase(t *testing.T) {
	berlin, err := os.ReadFile("/usr/share/zoneinfo/Europe/Berlin")
	if err != nil {
		t.Skip("no zoneinfo to build a database from:", err)
	}
	dir := t.TempDir()
	db := filepath.Join(dir, "tzdata")
	names := []string{"America/Denver", "America/Indiana/Knox", "Europe/Berlin", "UTC"}
	var index, data []byte
	for _, n := range names {
		entry := make([]byte, 52)
		copy(entry, n)
		binary.BigEndian.PutUint32(entry[40:], uint32(len(data)))
		binary.BigEndian.PutUint32(entry[44:], uint32(len(berlin)))
		index = append(index, entry...)
		data = append(data, berlin...)
	}
	head := make([]byte, 24)
	copy(head, "tzdata2026a")
	binary.BigEndian.PutUint32(head[12:], 24)
	binary.BigEndian.PutUint32(head[16:], uint32(24+len(index)))
	binary.BigEndian.PutUint32(head[20:], uint32(24+len(index)+len(data)))
	if err := os.WriteFile(db, append(append(head, index...), data...), 0o644); err != nil {
		t.Fatal(err)
	}
	oldDB, oldName, oldLink := zoneDB, nameFile, linkFile
	zoneDB, nameFile, linkFile = db, filepath.Join(dir, "timezone"), filepath.Join(dir, "localtime")
	t.Cleanup(func() { zoneDB, nameFile, linkFile = oldDB, oldName, oldLink })
	drainRestarts()

	if !HasDatabase() {
		t.Error("HasDatabase() = false with Android's database there")
	}
	if got := Regions(); !reflect.DeepEqual(got, []string{"America", "Europe"}) {
		t.Errorf("Regions() = %v", got)
	}
	if got := Zones("America"); !reflect.DeepEqual(got, []string{"Denver", "Indiana/Knox"}) {
		t.Errorf("Zones(America) = %v", got)
	}

	z := &Zone{}
	if err := z.apply("Europe/Berlin"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(linkFile)
	if err != nil || string(got) != string(berlin) {
		t.Fatalf("localtime is not the zone's data: %v", err)
	}
	loc, err := time.LoadLocationFromTZData("Local", got)
	if err != nil {
		t.Fatal(err)
	}
	if name, _ := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC).In(loc).Zone(); name != "CEST" {
		t.Errorf("zone in July = %s, want CEST", name)
	}
	if why := restartAsked(); why != "time zone Europe/Berlin" {
		t.Errorf("restart asked for %q", why)
	}
	if err := z.apply("Nowhere/Atlantis"); err == nil {
		t.Error("a zone the database does not have was applied")
	}
}

// A tablet without the database (none found where Android keeps it) has the common zones only.
func TestNoAndroidZoneDatabase(t *testing.T) {
	old := zoneDB
	zoneDB = filepath.Join(t.TempDir(), "tzdata")
	t.Cleanup(func() { zoneDB = old })
	if HasDatabase() {
		t.Error("HasDatabase() = true with no database")
	}
}
