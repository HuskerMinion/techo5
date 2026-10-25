package timezone

import (
	"testing"
)

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
