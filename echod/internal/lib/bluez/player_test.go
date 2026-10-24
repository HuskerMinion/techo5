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
	if !ok || st != (PlayerStatus{Device: "/org/bluez/hci0/dev_11_22_33_44_55_66", Status: "paused",
		Player: "/org/bluez/hci0/dev_11_22_33_44_55_66/avrcp/player0"}) {
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

// A Status is followed only from the player Control asks: the one the device's MediaControl1 names,
// kept current as it moves to another app; another app's player pausing is not the music pausing.
func TestOnlyTheAddressedPlayersStatusIsFollowed(t *testing.T) {
	dev := dbus.ObjectPath("/org/bluez/hci0/dev_11_22_33_44_55_66")
	p0, p1 := dev+"/avrcp/player0", dev+"/avrcp/player1"
	a := &Adapter{devices: map[dbus.ObjectPath]*Device{}, players: map[dbus.ObjectPath]map[string]map[string]dbus.Variant{}}
	var heard []PlayerStatus
	a.PlayerChanged.Listen(func(st PlayerStatus) { heard = append(heard, st) })
	props := func(path dbus.ObjectPath, ifc string, props map[string]dbus.Variant, gone ...string) {
		a.signal(&dbus.Signal{Path: path, Name: propsIfc + ".PropertiesChanged", Body: []any{ifc, props, gone}})
	}
	status := func(path dbus.ObjectPath, st string) {
		props(path, playerIfc, map[string]dbus.Variant{"Status": dbus.MakeVariant(st)})
	}
	a.signal(&dbus.Signal{Name: objMgrIfc + ".InterfacesAdded", Body: []any{dev, map[string]map[string]dbus.Variant{
		controlIfc: {"Player": dbus.MakeVariant(p1)},
	}}})
	for _, p := range []dbus.ObjectPath{p0, p1} {
		a.signal(&dbus.Signal{Name: objMgrIfc + ".InterfacesAdded", Body: []any{p, map[string]map[string]dbus.Variant{playerIfc: {}}}})
	}

	status(p0, "paused")
	status(p1, "playing")
	if len(heard) != 1 || heard[0].Player != p1 || heard[0].Status != "playing" {
		t.Fatalf("heard %+v, want only the addressed player playing", heard)
	}
	props(dev, controlIfc, map[string]dbus.Variant{"Player": dbus.MakeVariant(p0)})
	status(p1, "stopped")
	status(p0, "paused")
	if len(heard) != 2 || heard[1].Player != p0 || heard[1].Status != "paused" {
		t.Fatalf("heard %+v, want the newly addressed player paused", heard)
	}
	props(dev, controlIfc, map[string]dbus.Variant{}, "Player")
	a.signal(&dbus.Signal{Name: objMgrIfc + ".InterfacesRemoved", Body: []any{p0, []string{playerIfc}}})
	status(p1, "playing")
	if len(heard) != 3 || heard[2].Player != p1 {
		t.Fatalf("heard %+v, want the only player left followed", heard)
	}
}

// A device takes its volume from here when one of its own transports has a Volume: not when its
// transport has none, and not because another device's has one.
func TestAbsoluteVolumeIsTheDevicesTransportHavingAVolume(t *testing.T) {
	dev := dbus.ObjectPath("/org/bluez/hci0/dev_11_22_33_44_55_66")
	other := dbus.ObjectPath("/org/bluez/hci0/dev_66_55_44_33_22_11")
	with := map[string]dbus.Variant{"Volume": dbus.MakeVariant(uint16(88)), "State": dbus.MakeVariant("active")}
	without := map[string]dbus.Variant{"State": dbus.MakeVariant("active")}
	objs := func(path dbus.ObjectPath, props map[string]dbus.Variant) map[dbus.ObjectPath]map[string]map[string]dbus.Variant {
		return map[dbus.ObjectPath]map[string]map[string]dbus.Variant{
			dev:                {deviceIfc: {}},
			path + "/sep2/fd0": {transIfc: props},
		}
	}
	if !absoluteVolume(objs(dev, with), dev) {
		t.Error("a transport with a Volume was not taken as absolute volume")
	}
	if absoluteVolume(objs(dev, without), dev) {
		t.Error("a transport with no Volume was taken as absolute volume")
	}
	if absoluteVolume(objs(other, with), dev) {
		t.Error("another device's transport counted for this one")
	}
}
