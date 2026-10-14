package bluez

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

// A player's Status change is read with the device the player is under; anything else is not one.
func TestAPlayersStatusIsReadWithItsDevice(t *testing.T) {
	sig := func(ifc string, props map[string]dbus.Variant) *dbus.Signal {
		return &dbus.Signal{
			Path: "/org/bluez/hci0/dev_11_22_33_44_55_66/avrcp/player0",
			Name: propsIfc + ".PropertiesChanged",
			Body: []any{ifc, props, []string{}},
		}
	}
	st, ok := playerStatus(sig(playerIfc, map[string]dbus.Variant{"Status": dbus.MakeVariant("paused")}))
	if !ok || st != (PlayerStatus{Device: "/org/bluez/hci0/dev_11_22_33_44_55_66", Status: "paused"}) {
		t.Errorf("read %+v %v, want the device paused", st, ok)
	}
	if _, ok := playerStatus(sig(playerIfc, map[string]dbus.Variant{"Position": dbus.MakeVariant(uint32(5))})); ok {
		t.Error("a change with no Status was read as one")
	}
	if _, ok := playerStatus(sig(deviceIfc, map[string]dbus.Variant{"Status": dbus.MakeVariant("paused")})); ok {
		t.Error("a device's change was read as a player's")
	}
	if got := deviceOf("/org/bluez/hci0/dev_11_22_33_44_55_66"); got != "/org/bluez/hci0/dev_11_22_33_44_55_66" {
		t.Errorf("a device's own path came back as %s", got)
	}
}

// The player asked is the one the device's MediaControl1 names, and with none named the first by
// path, every time.
func TestThePlayerAskedIsTheAddressedOne(t *testing.T) {
	dev := dbus.ObjectPath("/org/bluez/hci0/dev_11_22_33_44_55_66")
	p0, p1 := dev+"/avrcp/player0", dev+"/avrcp/player1"
	objs := map[dbus.ObjectPath]map[string]map[string]dbus.Variant{
		dev: {controlIfc: {"Player": dbus.MakeVariant(p1)}},
		p0:  {playerIfc: {}},
		p1:  {playerIfc: {}},
		"/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF/avrcp/player0": {playerIfc: {}},
	}
	if got, ok := playerOf(objs, dev); !ok || got != p1 {
		t.Errorf("asked %s, want the addressed %s", got, p1)
	}
	delete(objs[dev], controlIfc)
	for range 5 {
		if got, ok := playerOf(objs, dev); !ok || got != p0 {
			t.Fatalf("asked %s with none addressed, want %s", got, p0)
		}
	}
	if _, ok := playerOf(objs, "/org/bluez/hci0/dev_00_00_00_00_00_00"); ok {
		t.Error("a device with no player found one")
	}
}
