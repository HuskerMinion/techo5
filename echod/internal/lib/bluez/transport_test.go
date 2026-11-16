//go:build linux

package bluez

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

// A speaker takes absolute volume once its transport has a Volume; before AVRCP says so, or on a
// speaker that never does, it has none. Another device's transport does not count.
func TestAbsoluteVolumeIsATransportsVolume(t *testing.T) {
	const dev = dbus.ObjectPath("/org/bluez/hci0/dev_11_22_33_44_55_66")
	transport := func(device dbus.ObjectPath, volume bool) map[string]map[string]dbus.Variant {
		props := map[string]dbus.Variant{"Device": dbus.MakeVariant(device), "State": dbus.MakeVariant("active")}
		if volume {
			props["Volume"] = dbus.MakeVariant(uint16(64))
		}
		return map[string]map[string]dbus.Variant{transportIfc: props}
	}
	objs := map[dbus.ObjectPath]map[string]map[string]dbus.Variant{
		dev:          {deviceIfc: {"Connected": dbus.MakeVariant(true)}},
		dev + "/fd0": transport(dev, false),
	}
	if transportVolume(objs, dev) {
		t.Error("a transport with no Volume took absolute volume")
	}
	objs["/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF/fd1"] = transport("/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF", true)
	if transportVolume(objs, dev) {
		t.Error("another device's Volume counted")
	}
	objs[dev+"/fd0"] = transport(dev, true)
	if !transportVolume(objs, dev) {
		t.Error("a transport with a Volume did not take absolute volume")
	}
}
