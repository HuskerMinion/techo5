package all

import (
	"slices"
	"strings"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
)

// actions is the actions that have to be there on every device, whatever the build tag. It is not
// the whole list of actions the daemon offers: most of them are a feature's own business and come
// and go with the device (the camera and slideshow ones are the Show's, and so on). What belongs
// here is an action another device depends on finding.
//
// These are not entities, so TestEveryComponentStillRegisters cannot see them. A feature whose whole
// interface is an action - announcements are one - can drop out of a build entirely and every other
// test here still passes.
var actions = []string{
	"announce_house",
	// Settings are copied from any device to the others, so every device has it, a Dot too.
	"copy_settings",
	// An automation that notifies every device calls each one's, so a device without them - a Dot,
	// where nothing else imports notify - fails the whole automation.
	"notify",
	"notify_picture",
	"notify_sized",
	"notify_picture_sized",
}

// The actions other devices depend on still register.
//
// announce_house did not, on the Dot. Nothing imported the announce package on a device with no
// screen - its only importers were display files, all of them built for the Show and the Spot - so
// its init never ran, the component never registered, and the Dot never advertised itself, never
// took an announcement and never offered the action. It looked like a network fault for a whole
// round of testing. Package all exists to stop exactly this, and announce was missing from it.
func TestTheCrossDeviceActionsStillRegister(t *testing.T) {
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

// What a device must never offer is not there, and the component that would offer it elsewhere has no
// actions at all on it: the test machine has no Wi-Fi, so the action list alone would pass whatever
// the build did.
func TestTheActionsNotOnThisDeviceAreNotThere(t *testing.T) {
	for _, a := range component.Default().Actions() {
		if _, ok := actionsNotOnThisDevice[a.Name]; ok {
			t.Errorf("action %q is offered on this device", a.Name)
		}
	}
	for name, owner := range actionsNotOnThisDevice {
		found := false
		for _, c := range component.Default().All() {
			if c.Name() != owner {
				continue
			}
			found = true
			if _, ok := c.(interface{ Actions() []*esphome.Action }); ok {
				t.Errorf("%s, which offers %s elsewhere, has actions on this device", owner, name)
			}
		}
		if !found {
			t.Errorf("no %s component to check for %s", owner, name)
		}
	}
}
