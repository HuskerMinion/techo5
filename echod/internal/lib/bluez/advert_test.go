//go:build linux

package bluez

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestAdvertOnly(t *testing.T) {
	v := dbus.MakeVariant(int16(-60))
	cases := []struct {
		props map[string]dbus.Variant
		want  bool
	}{
		{map[string]dbus.Variant{"RSSI": v}, true},
		{map[string]dbus.Variant{"RSSI": v, "ManufacturerData": v, "ServiceData": v, "TxPower": v}, true},
		{map[string]dbus.Variant{"RSSI": v, "Name": v}, false},
		{map[string]dbus.Variant{"Connected": v}, false},
		{map[string]dbus.Variant{"Paired": v, "RSSI": v}, false},
	}
	for _, c := range cases {
		if got := advertOnly(c.props); got != c.want {
			t.Errorf("advertOnly(%v) = %v, want %v", c.props, got, c.want)
		}
	}
}

// The adapter put back at its own path is Returned, once; another adapter, or a device, is not.
func TestTheAdapterComingBackIsReturned(t *testing.T) {
	a := &Adapter{path: "/org/bluez/hci0", devices: map[dbus.ObjectPath]*Device{}, players: map[dbus.ObjectPath]map[string]map[string]dbus.Variant{}}
	returned := 0
	a.Returned.Listen(func(struct{}) { returned++ })
	added := func(path dbus.ObjectPath, ifc string) *dbus.Signal {
		return &dbus.Signal{Name: objMgrIfc + ".InterfacesAdded",
			Body: []any{path, map[string]map[string]dbus.Variant{ifc: {}}}}
	}
	a.signal(added("/org/bluez/hci0/dev_00_11_22_33_44_55", deviceIfc))
	a.signal(added("/org/bluez/hci1", adapterIfc))
	if returned != 0 {
		t.Fatalf("a device or another adapter: returned %d", returned)
	}
	a.signal(added("/org/bluez/hci0", adapterIfc))
	if returned != 1 {
		t.Fatalf("hci0 back: returned %d, want 1", returned)
	}
}

// A sensor heard again reaches Advertised but not Changed; a connection change reaches Changed.
func TestAdvertisementIsNotAChange(t *testing.T) {
	path := dbus.ObjectPath("/org/bluez/hci0/dev_00_11_22_33_44_55")
	a := &Adapter{devices: map[dbus.ObjectPath]*Device{path: {Path: path}}}
	changes, heard := 0, 0
	a.Changed.Listen(func(struct{}) { changes++ })
	a.Advertised.Listen(func(Device) { heard++ })

	props := func(k string, v any) []any {
		return []any{deviceIfc, map[string]dbus.Variant{k: dbus.MakeVariant(v)}, []string{}}
	}
	a.signal(&dbus.Signal{Path: path, Name: propsIfc + ".PropertiesChanged", Body: props("RSSI", int16(-50))})
	if changes != 0 || heard != 1 {
		t.Fatalf("RSSI: changes=%d heard=%d, want 0 and 1", changes, heard)
	}
	a.signal(&dbus.Signal{Path: path, Name: propsIfc + ".PropertiesChanged", Body: props("Connected", true)})
	if changes != 1 {
		t.Fatalf("Connected: changes=%d, want 1", changes)
	}
}
