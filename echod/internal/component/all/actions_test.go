package all

import (
	"slices"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/component"
)

// actions is every action this device offers Home Assistant, on every device.
//
// These are not entities, so TestEveryComponentStillRegisters cannot see them. A feature whose whole
// interface is an action - announcements are one - can drop out of a build entirely and every other
// test here still passes.
var actions = []string{
	"announce_house",
}

// Every action still registers.
//
// announce_house did not, on the Dot. Nothing imported the announce package on a device with no
// screen - its only importers were display files, all of them built for the Show and the Spot - so
// its init never ran, the component never registered, and the Dot never advertised itself, never
// took an announcement and never offered the action. It looked like a network fault for a whole
// round of testing. Package all exists to stop exactly this, and announce was missing from it.
func TestEveryActionStillRegisters(t *testing.T) {
	var got []string
	for _, a := range component.Default().Actions() {
		got = append(got, a.Name)
	}
	slices.Sort(got)

	want := slices.Clone(actions)
	slices.Sort(want)

	for _, name := range want {
		if !slices.Contains(got, name) {
			t.Errorf("action %q is not registered on this device; have: %s", name, strings.Join(got, " "))
		}
	}
}

// Announcements are a device-to-device feature, so every device has to be in it: one that does not
// register is one the others cannot reach and one that cannot reach them. The Dot is the case that
// matters, since it is the one with no screen to notice with.
func TestAnnouncementsAreOnEveryDevice(t *testing.T) {
	for _, c := range component.Default().All() {
		if c.Name() == "announcements" {
			return
		}
	}
	t.Error("the announcements component is not registered on this device")
}
