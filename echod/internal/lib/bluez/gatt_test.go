//go:build linux

package bluez

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestHandleOf(t *testing.T) {
	for path, want := range map[dbus.ObjectPath]uint16{
		"/org/bluez/hci0/dev_C0_FF_EE_12_34_56/service000c":                   0x0c,
		"/org/bluez/hci0/dev_C0_FF_EE_12_34_56/service000c/char000d":          0x0d,
		"/org/bluez/hci0/dev_C0_FF_EE_12_34_56/service000c/char000d/desc00ff": 0xff,
	} {
		if h, ok := handleOf(path); !ok || h != want {
			t.Errorf("handleOf(%s) = %#x %v, want %#x", path, h, ok, want)
		}
	}
	if _, ok := handleOf("/org/bluez/hci0/dev_C0_FF_EE_12_34_56"); ok {
		t.Error("a device path has no handle")
	}
}

func TestTree(t *testing.T) {
	dev := dbus.ObjectPath("/org/bluez/hci0/dev_C0_FF_EE_12_34_56")
	str := func(s string) dbus.Variant { return dbus.MakeVariant(s) }
	objs := map[dbus.ObjectPath]map[string]map[string]dbus.Variant{
		dev:                  {deviceIfc: {"Address": str("C0:FF:EE:12:34:56")}},
		dev + "/service0010": {gattServiceIfc: {"UUID": str("0000180A-0000-1000-8000-00805F9B34FB")}},
		dev + "/service000c": {gattServiceIfc: {"UUID": str("f00dcafe-0000-4000-8000-000000000001")}},
		dev + "/service000c/char000d": {gattCharIfc: {
			"UUID":  str("f00dcafe-0000-4000-8000-000000000002"),
			"Flags": dbus.MakeVariant([]string{"read", "notify"}),
		}},
		dev + "/service000c/char000d/desc000f": {gattDescIfc: {"UUID": str(cccd)}},
		// Another device's attributes stay out.
		"/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF/service0001": {gattServiceIfc: {"UUID": str("1800")}},
	}

	services, attrs := tree(dev, objs)
	if len(services) != 2 || services[0].Handle != 0x0c || services[1].Handle != 0x10 {
		t.Fatalf("services %+v", services)
	}
	if services[1].UUID != "0000180a-0000-1000-8000-00805f9b34fb" {
		t.Errorf("uuid not lower case: %s", services[1].UUID)
	}
	chars := services[0].Characteristics
	if len(chars) != 1 || chars[0].Handle != 0x0e || len(chars[0].Flags) != 2 {
		t.Fatalf("characteristics %+v", chars)
	}
	if len(chars[0].Descriptors) != 1 || chars[0].Descriptors[0].Handle != 0x0f {
		t.Fatalf("descriptors %+v", chars[0].Descriptors)
	}
	if a := attrs[0x0f]; a.ifc != gattDescIfc || a.char != 0x0e || a.uuid != cccd {
		t.Errorf("descriptor attribute %+v", a)
	}
	if len(attrs) != 4 {
		t.Errorf("attributes %d, want 4", len(attrs))
	}
}

func TestLinksGoWithTheirDevice(t *testing.T) {
	for _, s := range []*dbus.Signal{
		{Name: objMgrIfc + ".InterfacesRemoved", Body: []any{dbus.ObjectPath("/org/bluez/hci0/dev_C0_FF_EE_12_34_56"), []string{deviceIfc}}},
		{Name: objMgrIfc + ".InterfacesRemoved", Body: []any{dbus.ObjectPath("/org/bluez/hci0"), []string{adapterIfc}}},
		{Name: "org.freedesktop.DBus.NameOwnerChanged", Body: []any{service, ":1.0", ""}},
		{Name: propsIfc + ".PropertiesChanged", Path: "/org/bluez/hci0/dev_C0_FF_EE_12_34_56",
			Body: []any{deviceIfc, map[string]dbus.Variant{"Connected": dbus.MakeVariant(false)}, []string{}}},
	} {
		var lost []string
		stopped := false
		c := &Central{adapter: "/org/bluez/hci0", lost: func(a string) { lost = append(lost, a) }, links: map[string]*link{
			"C0:FF:EE:12:34:56": {path: "/org/bluez/hci0/dev_C0_FF_EE_12_34_56", notifying: map[uint16]func(){0x0e: func() { stopped = true }}},
			"AA:BB:CC:DD:EE:FF": {path: "/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF", notifying: map[uint16]func(){}},
		}}
		c.signal(s)
		if len(lost) == 0 || lost[0] != "C0:FF:EE:12:34:56" && lost[len(lost)-1] != "C0:FF:EE:12:34:56" || !stopped {
			t.Errorf("%s: lost %v, notifications stopped %v", s.Name, lost, stopped)
		}
		if _, still := c.links["C0:FF:EE:12:34:56"]; still {
			t.Errorf("%s: link kept", s.Name)
		}
	}
}
