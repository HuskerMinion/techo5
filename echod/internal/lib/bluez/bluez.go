//go:build linux

// Package bluez talks to bluetoothd over D-Bus: the adapter's modes, the devices it knows or is
// finding, pairing and connecting them, and a pairing agent that says yes.
//
// This is the classic (BR/EDR) side of the radio for earbuds and speakers. The BLE proxy is a
// different thing on a different path (hardware/ble) and must not run at the same time.
package bluez

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
)

const (
	service     = "org.bluez"
	adapterIfc  = "org.bluez.Adapter1"
	deviceIfc   = "org.bluez.Device1"
	playerIfc   = "org.bluez.MediaPlayer1"
	controlIfc  = "org.bluez.MediaControl1"
	agentIfc    = "org.bluez.Agent1"
	agentMgrIfc = "org.bluez.AgentManager1"
	propsIfc    = "org.freedesktop.DBus.Properties"
	objMgrIfc   = "org.freedesktop.DBus.ObjectManager"

	agentPath = dbus.ObjectPath("/org/techo5/agent")

	// AudioSink is the A2DP sink service class: something that plays what it is sent.
	AudioSink = "0000110b-0000-1000-8000-00805f9b34fb"
	// AudioSource is the A2DP source service class: something that sends audio, a phone.
	AudioSource = "0000110a-0000-1000-8000-00805f9b34fb"
)

// SystemBus is where bluetoothd is. Set from the environment if it is somewhere else.
func SystemBus() (*dbus.Conn, error) {
	if addr := os.Getenv("DBUS_SYSTEM_BUS_ADDRESS"); addr != "" {
		return dbus.Connect(addr)
	}
	for _, p := range []string{"/run/dbus/system_bus_socket", "/var/run/dbus/system_bus_socket"} {
		if _, err := os.Stat(p); err == nil {
			return dbus.Connect("unix:path=" + p)
		}
	}
	return dbus.SystemBus()
}

// Device is one remote the adapter knows about.
type Device struct {
	Path        dbus.ObjectPath
	Address     string
	AddressType string // "public" or "random"
	Name        string
	Icon        string
	RSSI        int16
	Paired      bool
	Trusted     bool
	Connected   bool
	AudioSink   bool // offers A2DP sink: earbuds, a speaker

	// What the last advertisement carried, for the proxy.
	UUIDs            []string
	ManufacturerData map[uint16][]byte
	ServiceData      map[string][]byte
}

// Adapter is hci0 as bluetoothd presents it.
type Adapter struct {
	conn *dbus.Conn
	path dbus.ObjectPath
	obj  dbus.BusObject

	// Changed fires when a device appears, goes, or changes, but not when it is only heard again
	// (that is Advertised); listeners must not block.
	Changed hook.Hook[struct{}]

	// Advertised fires with a device every time one is heard from: it appeared, or its signal or
	// advertising data changed. Listeners must not block.
	Advertised hook.Hook[Device]

	// PlayerChanged fires when a device's AVRCP player (a phone playing to this one) says it is
	// playing or not. Listeners must not block.
	PlayerChanged hook.Hook[PlayerStatus]

	mu      sync.Mutex
	devices map[dbus.ObjectPath]*Device
	agent   *agent
	stop    func()
}

// Open connects to bluetoothd and takes the first adapter. It fails while bluetoothd or the
// controller is not there yet; the caller retries.
func Open(ctx context.Context) (*Adapter, error) {
	conn, err := SystemBus()
	if err != nil {
		return nil, fmt.Errorf("system bus: %w", err)
	}
	a := &Adapter{conn: conn, devices: map[dbus.ObjectPath]*Device{}}
	if err := a.load(); err != nil {
		conn.Close()
		return nil, err
	}
	if a.path == "" {
		conn.Close()
		return nil, errors.New("bluez: no adapter")
	}
	a.obj = conn.Object(service, a.path)
	if err := a.watch(); err != nil {
		conn.Close()
		return nil, err
	}
	return a, nil
}

// Close lets the bus go.
func (a *Adapter) Close() error {
	if a.stop != nil {
		a.stop()
	}
	if a.agent != nil {
		_ = a.conn.Object(service, "/org/bluez").Call(agentMgrIfc+".UnregisterAgent", 0, agentPath).Err
	}
	return a.conn.Close()
}

// load reads everything bluetoothd exports: the adapter and the devices under it.
func (a *Adapter) load() error {
	var objs map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	if err := a.conn.Object(service, "/").Call(objMgrIfc+".GetManagedObjects", 0).Store(&objs); err != nil {
		return fmt.Errorf("bluez: managed objects: %w", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.devices = map[dbus.ObjectPath]*Device{}
	for path, ifcs := range objs {
		if _, ok := ifcs[adapterIfc]; ok && (a.path == "" || path < a.path) {
			a.path = path
		}
		if props, ok := ifcs[deviceIfc]; ok {
			a.devices[path] = deviceFrom(path, props)
		}
	}
	return nil
}

func deviceFrom(path dbus.ObjectPath, props map[string]dbus.Variant) *Device {
	d := &Device{Path: path}
	d.update(props)
	return d
}

func (d *Device) update(props map[string]dbus.Variant) {
	for k, v := range props {
		switch k {
		case "Address":
			d.Address, _ = v.Value().(string)
		case "AddressType":
			d.AddressType, _ = v.Value().(string)
		case "ManufacturerData":
			if m, ok := v.Value().(map[uint16]dbus.Variant); ok {
				d.ManufacturerData = map[uint16][]byte{}
				for k, vv := range m {
					if b, ok := vv.Value().([]byte); ok {
						d.ManufacturerData[k] = b
					}
				}
			}
		case "ServiceData":
			if m, ok := v.Value().(map[string]dbus.Variant); ok {
				d.ServiceData = map[string][]byte{}
				for k, vv := range m {
					if b, ok := vv.Value().([]byte); ok {
						d.ServiceData[k] = b
					}
				}
			}
		case "Name":
			d.Name, _ = v.Value().(string)
		case "Alias":
			if d.Name == "" {
				d.Name, _ = v.Value().(string)
			}
		case "Icon":
			d.Icon, _ = v.Value().(string)
		case "RSSI":
			d.RSSI, _ = v.Value().(int16)
		case "Paired":
			d.Paired, _ = v.Value().(bool)
		case "Trusted":
			d.Trusted, _ = v.Value().(bool)
		case "Connected":
			d.Connected, _ = v.Value().(bool)
		case "UUIDs":
			if uuids, ok := v.Value().([]string); ok {
				d.UUIDs = uuids
				d.AudioSink = false
				for _, u := range uuids {
					if strings.EqualFold(u, AudioSink) {
						d.AudioSink = true
					}
				}
			}
		}
	}
}

// watch follows device changes so Devices stays current without polling.
func (a *Adapter) watch() error {
	opts := [][]dbus.MatchOption{
		{dbus.WithMatchInterface(objMgrIfc), dbus.WithMatchMember("InterfacesAdded")},
		{dbus.WithMatchInterface(objMgrIfc), dbus.WithMatchMember("InterfacesRemoved")},
		{dbus.WithMatchInterface(propsIfc), dbus.WithMatchMember("PropertiesChanged"), dbus.WithMatchArg(0, deviceIfc)},
		{dbus.WithMatchInterface(propsIfc), dbus.WithMatchMember("PropertiesChanged"), dbus.WithMatchArg(0, playerIfc)},
	}
	for _, o := range opts {
		if err := a.conn.AddMatchSignal(o...); err != nil {
			return fmt.Errorf("bluez: match: %w", err)
		}
	}
	ch := make(chan *dbus.Signal, 64)
	a.conn.Signal(ch)
	ctx, cancel := context.WithCancel(context.Background())
	a.stop = cancel
	go func() {
		for {
			select {
			case <-ctx.Done():
				a.conn.RemoveSignal(ch)
				return
			case s := <-ch:
				if s == nil {
					return
				}
				a.signal(s)
			}
		}
	}()
	return nil
}

// PlayerStatus is a device's AVRCP player saying what it is doing: MediaPlayer1's Status,
// "playing", "paused", "stopped", "forward-seek", "reverse-seek" or "error".
type PlayerStatus struct {
	Device dbus.ObjectPath
	Status string
}

// playerStatus reads a player's Status change out of a signal, with the device the player is under
// (BlueZ puts it at the device's path, then avrcp/playerN).
func playerStatus(s *dbus.Signal) (PlayerStatus, bool) {
	if s.Name != propsIfc+".PropertiesChanged" || len(s.Body) < 2 {
		return PlayerStatus{}, false
	}
	if ifc, _ := s.Body[0].(string); ifc != playerIfc {
		return PlayerStatus{}, false
	}
	props, _ := s.Body[1].(map[string]dbus.Variant)
	status, ok := props["Status"].Value().(string)
	if !ok {
		return PlayerStatus{}, false
	}
	return PlayerStatus{Device: deviceOf(s.Path), Status: status}, true
}

// deviceOf is the device path an object under it belongs to: /org/bluez/hci0/dev_XX for
// /org/bluez/hci0/dev_XX/avrcp/player0.
func deviceOf(path dbus.ObjectPath) dbus.ObjectPath {
	p := string(path)
	i := strings.Index(p, "/dev_")
	if i < 0 {
		return path
	}
	if j := strings.Index(p[i+1:], "/"); j >= 0 {
		return dbus.ObjectPath(p[:i+1+j])
	}
	return path
}

// ErrNoPlayer is a device with no AVRCP player to ask: not connected, or not a phone that offers
// one.
var ErrNoPlayer = errors.New("bluez: the device has no media player")

// Control asks a device's AVRCP player, a phone playing to this one, to play, pause or skip: method
// is MediaPlayer1's Play, Pause, Next or Previous.
func (a *Adapter) Control(ctx context.Context, device dbus.ObjectPath, method string) error {
	var objs map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	if err := a.conn.Object(service, "/").CallWithContext(ctx, objMgrIfc+".GetManagedObjects", 0).Store(&objs); err != nil {
		return err
	}
	player, ok := playerOf(objs, device)
	if !ok {
		return ErrNoPlayer
	}
	return a.conn.Object(service, player).CallWithContext(ctx, playerIfc+"."+method, 0).Err
}

// playerOf is the player of device's to ask: the one its MediaControl1 says is the phone's
// addressed player, the app in control, or with none said, the first of its players by path, so the
// same one is asked every time.
func playerOf(objs map[dbus.ObjectPath]map[string]map[string]dbus.Variant, device dbus.ObjectPath) (dbus.ObjectPath, bool) {
	if p, ok := objs[device][controlIfc]["Player"].Value().(dbus.ObjectPath); ok {
		if _, ok := objs[p][playerIfc]; ok {
			return p, true
		}
	}
	var players []string
	for path, ifcs := range objs {
		if _, ok := ifcs[playerIfc]; ok && deviceOf(path) == device {
			players = append(players, string(path))
		}
	}
	if len(players) == 0 {
		return "", false
	}
	sort.Strings(players)
	return dbus.ObjectPath(players[0]), true
}

// advertOnly is a change that is only a device heard again: its signal strength or what it
// advertises. Bluetooth sensors send these many times a second without anything about the device as
// a connection changing, so they go to Advertised alone and not to Changed.
func advertOnly(props map[string]dbus.Variant) bool {
	for k := range props {
		switch k {
		case "RSSI", "TxPower", "ManufacturerData", "ServiceData":
		default:
			return false
		}
	}
	return true
}

func (a *Adapter) signal(s *dbus.Signal) {
	if st, ok := playerStatus(s); ok {
		a.PlayerChanged.Emit(st)
		return
	}
	changed := false
	var heard *Device
	a.mu.Lock()
	switch s.Name {
	case objMgrIfc + ".InterfacesAdded":
		if len(s.Body) == 2 {
			path, _ := s.Body[0].(dbus.ObjectPath)
			ifcs, _ := s.Body[1].(map[string]map[string]dbus.Variant)
			if props, ok := ifcs[deviceIfc]; ok {
				a.devices[path] = deviceFrom(path, props)
				changed = true
				cp := *a.devices[path]
				heard = &cp
			}
		}
	case objMgrIfc + ".InterfacesRemoved":
		if len(s.Body) == 2 {
			path, _ := s.Body[0].(dbus.ObjectPath)
			ifcs, _ := s.Body[1].([]string)
			for _, i := range ifcs {
				if i == deviceIfc {
					delete(a.devices, path)
					changed = true
				}
			}
		}
	case propsIfc + ".PropertiesChanged":
		if len(s.Body) >= 2 {
			if ifc, _ := s.Body[0].(string); ifc == deviceIfc {
				if d, ok := a.devices[s.Path]; ok {
					props, _ := s.Body[1].(map[string]dbus.Variant)
					d.update(props)
					changed = !advertOnly(props)
					for _, k := range []string{"RSSI", "ManufacturerData", "ServiceData"} {
						if _, ok := props[k]; ok {
							cp := *d
							heard = &cp
							break
						}
					}
				}
			}
		}
	}
	a.mu.Unlock()
	if changed {
		a.Changed.Emit(struct{}{})
	}
	if heard != nil {
		a.Advertised.Emit(*heard)
	}
}

// DiscoverLE starts (or stops) discovery of Low Energy devices with every advertisement
// reported, duplicates included, which is what a proxy scanning for Home Assistant wants.
func (a *Adapter) DiscoverLE(on bool) error {
	if !on {
		return a.Discover(false)
	}
	filter := map[string]dbus.Variant{
		"Transport":     dbus.MakeVariant("le"),
		"DuplicateData": dbus.MakeVariant(true),
	}
	if err := a.obj.Call(adapterIfc+".SetDiscoveryFilter", 0, filter).Err; err != nil {
		slog.Debug("le discovery filter", "err", err)
	}
	err := a.obj.Call(adapterIfc+".StartDiscovery", 0).Err
	if err != nil && strings.Contains(err.Error(), "InProgress") {
		return nil
	}
	return err
}

// Devices is what the adapter knows, strongest signal first.
func (a *Adapter) Devices() []Device {
	a.mu.Lock()
	out := make([]Device, 0, len(a.devices))
	for _, d := range a.devices {
		out = append(out, *d)
	}
	a.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Connected != out[j].Connected {
			return out[i].Connected
		}
		if out[i].Paired != out[j].Paired {
			return out[i].Paired
		}
		return out[i].RSSI > out[j].RSSI
	})
	return out
}

// Device finds one by address.
func (a *Adapter) Device(address string) (Device, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, d := range a.devices {
		if strings.EqualFold(d.Address, address) {
			return *d, true
		}
	}
	return Device{}, false
}

func (a *Adapter) set(prop string, v any) error {
	return a.obj.Call(propsIfc+".Set", 0, adapterIfc, prop, dbus.MakeVariant(v)).Err
}

func (a *Adapter) get(prop string) (dbus.Variant, error) {
	var v dbus.Variant
	err := a.obj.Call(propsIfc+".Get", 0, adapterIfc, prop).Store(&v)
	return v, err
}

// Powered turns the controller on or off.
func (a *Adapter) Powered(on bool) error { return a.set("Powered", on) }

// IsPowered reports the controller's state.
func (a *Adapter) IsPowered() bool {
	v, err := a.get("Powered")
	if err != nil {
		return false
	}
	on, _ := v.Value().(bool)
	return on
}

// Alias is the name other devices see.
func (a *Adapter) Alias(name string) error { return a.set("Alias", name) }

// Address is the controller's own.
func (a *Adapter) Address() string {
	v, err := a.get("Address")
	if err != nil {
		return ""
	}
	s, _ := v.Value().(string)
	return s
}

// Pairing makes the adapter findable and willing to bond, or neither. Discoverable mode has no
// timeout of its own here: the caller ends it.
func (a *Adapter) Pairing(on bool) error {
	if err := a.set("DiscoverableTimeout", uint32(0)); err != nil {
		return err
	}
	if err := a.set("Pairable", on); err != nil {
		return err
	}
	return a.set("Discoverable", on)
}

// Discover starts or stops looking for devices. Scanning shares the antenna with Wi-Fi, so it is
// only on while someone is pairing.
func (a *Adapter) Discover(on bool) error {
	if on {
		filter := map[string]dbus.Variant{"Transport": dbus.MakeVariant("auto")}
		if err := a.obj.Call(adapterIfc+".SetDiscoveryFilter", 0, filter).Err; err != nil {
			slog.Debug("discovery filter", "err", err)
		}
		err := a.obj.Call(adapterIfc+".StartDiscovery", 0).Err
		if err != nil && strings.Contains(err.Error(), "InProgress") {
			return nil
		}
		return err
	}
	err := a.obj.Call(adapterIfc+".StopDiscovery", 0).Err
	if err != nil && (strings.Contains(err.Error(), "NotReady") || strings.Contains(err.Error(), "Failed")) {
		return nil // nothing was running
	}
	return err
}

func (a *Adapter) device(address string) (dbus.BusObject, error) {
	d, ok := a.Device(address)
	if !ok {
		return nil, fmt.Errorf("bluez: no device %s", address)
	}
	return a.conn.Object(service, d.Path), nil
}

// Pair bonds with a device; bluetoothd asks the agent to confirm, which it does.
func (a *Adapter) Pair(ctx context.Context, address string) error {
	o, err := a.device(address)
	if err != nil {
		return err
	}
	err = o.CallWithContext(ctx, deviceIfc+".Pair", 0).Err
	if err != nil && strings.Contains(err.Error(), "AlreadyExists") {
		return nil
	}
	return err
}

// Trust lets the device connect on its own later, which earbuds do when they come out of the case.
func (a *Adapter) Trust(address string, on bool) error {
	o, err := a.device(address)
	if err != nil {
		return err
	}
	return o.Call(propsIfc+".Set", 0, deviceIfc, "Trusted", dbus.MakeVariant(on)).Err
}

// Connect brings up every profile the device offers, A2DP among them.
func (a *Adapter) Connect(ctx context.Context, address string) error {
	o, err := a.device(address)
	if err != nil {
		return err
	}
	return o.CallWithContext(ctx, deviceIfc+".Connect", 0).Err
}

// Disconnect drops the link.
func (a *Adapter) Disconnect(ctx context.Context, address string) error {
	o, err := a.device(address)
	if err != nil {
		return err
	}
	return o.CallWithContext(ctx, deviceIfc+".Disconnect", 0).Err
}

// Remove forgets the device and its keys.
func (a *Adapter) Remove(address string) error {
	d, ok := a.Device(address)
	if !ok {
		return nil
	}
	return a.obj.Call(adapterIfc+".RemoveDevice", 0, d.Path).Err
}

// RegisterAgent makes this process the pairing agent. While pairing (which reports whether pairing
// mode is on) says yes, a pairing is answered yes, which is what a device with no keyboard can do, and
// the passkey a phone shows is accepted as shown. At any other time pairing is refused, so nothing
// nearby can bond with the device unasked. A connection bluetoothd asks about is allowed only for the
// audio profiles, whatever the device.
//
// paired, when set, hears the device path of each pairing the agent said yes to, and code the
// six-digit code of each one confirmed by number, which the other device shows too: said on the
// screen, so whoever is pairing a phone can see it is theirs.
func (a *Adapter) RegisterAgent(pairing func() bool, paired func(path string), code func(path, code string)) error {
	ag := &agent{pairing: pairing, paired: paired, code: code}
	if err := a.conn.Export(ag, agentPath, agentIfc); err != nil {
		return fmt.Errorf("bluez: export agent: %w", err)
	}
	mgr := a.conn.Object(service, "/org/bluez")
	if err := mgr.Call(agentMgrIfc+".RegisterAgent", 0, agentPath, "DisplayYesNo").Err; err != nil {
		if !strings.Contains(err.Error(), "AlreadyExists") {
			return fmt.Errorf("bluez: register agent: %w", err)
		}
	}
	if err := mgr.Call(agentMgrIfc+".RequestDefaultAgent", 0, agentPath).Err; err != nil {
		return fmt.Errorf("bluez: default agent: %w", err)
	}
	a.agent = ag
	return nil
}

// agent is org.bluez.Agent1. Methods return *dbus.Error; nil is yes.
type agent struct {
	pairing func() bool
	paired  func(path string)
	code    func(path, code string)
}

// audioProfiles are the service classes a connection may be authorized for: A2DP source and sink,
// AVRCP target and controller, and the A/V control and distribution transports under them.
var audioProfiles = map[string]bool{
	"0000110a-0000-1000-8000-00805f9b34fb": true, // Audio Source
	"0000110b-0000-1000-8000-00805f9b34fb": true, // Audio Sink
	"0000110c-0000-1000-8000-00805f9b34fb": true, // A/V Remote Control Target
	"0000110d-0000-1000-8000-00805f9b34fb": true, // Advanced Audio Distribution
	"0000110e-0000-1000-8000-00805f9b34fb": true, // A/V Remote Control
	"0000110f-0000-1000-8000-00805f9b34fb": true, // A/V Remote Control Controller
	"00000017-0000-1000-8000-00805f9b34fb": true, // AVCTP
	"00000019-0000-1000-8000-00805f9b34fb": true, // AVDTP
}

func rejected(what string) *dbus.Error {
	return dbus.NewError("org.bluez.Error.Rejected", []any{what})
}

// open is whether a pairing may go ahead now.
func (g agent) open(path dbus.ObjectPath, what string) *dbus.Error {
	if g.pairing != nil && g.pairing() {
		if g.paired != nil {
			g.paired(string(path))
		}
		return nil
	}
	slog.Warn("bluetooth: refused a pairing outside pairing mode", "device", string(path), "request", what)
	return rejected("not in pairing mode")
}

func (agent) Release() *dbus.Error { return nil }

func (g agent) RequestPinCode(path dbus.ObjectPath) (string, *dbus.Error) {
	if err := g.open(path, "pin"); err != nil {
		return "", err
	}
	return "0000", nil
}

func (agent) DisplayPinCode(dbus.ObjectPath, string) *dbus.Error { return nil }

func (g agent) RequestPasskey(path dbus.ObjectPath) (uint32, *dbus.Error) {
	if err := g.open(path, "passkey"); err != nil {
		return 0, err
	}
	return 0, nil
}

func (agent) DisplayPasskey(dbus.ObjectPath, uint32, uint16) *dbus.Error { return nil }

func (g agent) RequestConfirmation(path dbus.ObjectPath, passkey uint32) *dbus.Error {
	if err := g.open(path, "confirmation"); err != nil {
		return err
	}
	code := fmt.Sprintf("%06d", passkey)
	slog.Info("bluetooth pairing confirmed", "device", string(path), "passkey", code)
	if g.code != nil {
		g.code(string(path), code)
	}
	return nil
}

func (g agent) RequestAuthorization(path dbus.ObjectPath) *dbus.Error {
	return g.open(path, "authorization")
}

func (agent) AuthorizeService(path dbus.ObjectPath, uuid string) *dbus.Error {
	if audioProfiles[strings.ToLower(uuid)] {
		return nil
	}
	slog.Warn("bluetooth: refused a connection for a profile other than audio", "device", string(path), "uuid", uuid)
	return rejected("only audio profiles are allowed")
}

func (agent) Cancel() *dbus.Error { return nil }
