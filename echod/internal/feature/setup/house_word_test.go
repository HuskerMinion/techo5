package setup

import (
	"strings"
	"testing"
)

// A short house word is warned of, not refused; a long one and none are not.
func TestAShortHouseWordIsWarnedOf(t *testing.T) {
	if !strings.Contains(shortWordNote("lantern"), "short") {
		t.Error("a short word is not warned of")
	}
	if shortWordNote("") != "" || shortWordNote("a long phrase for this house") != "" {
		t.Error("an empty or long word is warned of")
	}
}

// Devices with another house word are named, and a name cannot put markup on the page.
func TestDevicesWithAnotherWordAreNamed(t *testing.T) {
	if otherWordNote(nil) != "" {
		t.Error("a note with no devices")
	}
	got := otherWordNote([]string{"Garage", "<b>Porch</b>"})
	if !strings.Contains(got, "2 other devices here use a different house word: Garage, &lt;b&gt;Porch&lt;/b&gt;") {
		t.Errorf("note %q", got)
	}
	if !strings.Contains(otherWordNote([]string{"Garage"}), "1 other device here uses") {
		t.Error("one device is not named as one")
	}
}

// Nearest device answers on with no house word is warned of, since it does nothing then.
func TestNearestWithNoWordIsWarnedOf(t *testing.T) {
	if !strings.Contains(nearestWordNote("", true), "does nothing") {
		t.Error("no warning with the switch on and no word")
	}
	if nearestWordNote("", false) != "" || nearestWordNote("a long phrase for this house", true) != "" {
		t.Error("a warning with the switch off or a word set")
	}
}
