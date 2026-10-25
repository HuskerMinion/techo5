//go:build linux

package bluez

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

const (
	gattServiceIfc = "org.bluez.GattService1"
	gattCharIfc    = "org.bluez.GattCharacteristic1"
	gattDescIfc    = "org.bluez.GattDescriptor1"

	// cccd is the Client Characteristic Configuration descriptor. bluetoothd owns it and refuses
	// writes to it; notifications are turned on and off through the characteristic instead.
	cccd = "00002902-0000-1000-8000-00805f9b34fb"

	// defaultMTU is what ATT starts at before an exchange, and what is reported when bluetoothd
	// does not say (MTU is a property only since BlueZ 5.62).
	defaultMTU = 23
)

// Service is one GATT service of a connected LE device. Handles are ATT handles, as an ESP32 proxy
// reports them: the service's first handle, a characteristic's value, a descriptor's own. Home
// Assistant keeps them between connections and whichever proxy made them, so they must match.
type Service struct {
	Handle          uint16
	UUID            string
	Characteristics []Characteristic
}

// Characteristic is one characteristic of a Service. Flags are bluetoothd's names for its
// properties ("read", "write-without-response", "notify", ...).
type Characteristic struct {
	Handle      uint16
	UUID        string
	Flags       []string
	Descriptors []Descriptor
}

// Descriptor is one descriptor of a Characteristic.
type Descriptor struct {
	Handle uint16
	UUID   string
}

// Central is the LE client side of bluetoothd: connections to other devices' GATT servers, for the
// Bluetooth proxy to carry Home Assistant's reads, writes and notifications. It has its own bus
// connection, so its signals and Adapter's don't mix.
type Central struct {
	conn    *dbus.Conn
	adapter dbus.ObjectPath
	value   func(address string, handle uint16, data []byte)
	lost    func(address string)
	stop    func()

	mu    sync.Mutex
	links map[string]*link // by upper-case address
}

// link is one connected device: where bluetoothd has it, its attributes by handle once read, and
// the notifications running on it.
type link struct {
	path      dbus.ObjectPath
	attrs     map[uint16]attribute
	notifying map[uint16]func()
	closing   bool
}

type attribute struct {
	path dbus.ObjectPath
	ifc  string
	uuid string
	char uint16 // a descriptor's characteristic
}

// OpenCentral connects to bluetoothd. value hears every notification and indication, and lost every
// device that disconnects without Disconnect being asked. Both are called on the bus's goroutines
// and must not block.
func OpenCentral(value func(address string, handle uint16, data []byte), lost func(address string)) (*Central, error) {
	conn, err := SystemBus()
	if err != nil {
		return nil, fmt.Errorf("system bus: %w", err)
	}
	c := &Central{conn: conn, value: value, lost: lost, links: map[string]*link{}}
	objs, err := c.objects()
	if err != nil {
		conn.Close()
		return nil, err
	}
	for path, ifcs := range objs {
		if _, ok := ifcs[adapterIfc]; ok && (c.adapter == "" || path < c.adapter) {
			c.adapter = path
		}
	}
	if c.adapter == "" {
		conn.Close()
		return nil, errors.New("bluez: no adapter")
	}
	if err := c.watch(); err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

// Close disconnects every device and lets the bus go.
func (c *Central) Close() error {
	c.mu.Lock()
	addrs := make([]string, 0, len(c.links))
	for a := range c.links {
		addrs = append(addrs, a)
	}
	c.mu.Unlock()
	for _, a := range addrs {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = c.Disconnect(ctx, a)
		cancel()
	}
	if c.stop != nil {
		c.stop()
	}
	return c.conn.Close()
}

func (c *Central) objects() (map[dbus.ObjectPath]map[string]map[string]dbus.Variant, error) {
	var objs map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	if err := c.conn.Object(service, "/").Call(objMgrIfc+".GetManagedObjects", 0).Store(&objs); err != nil {
		return nil, fmt.Errorf("bluez: managed objects: %w", err)
	}
	return objs, nil
}

func (c *Central) watch() error {
	opts := [][]dbus.MatchOption{
		{dbus.WithMatchInterface(propsIfc), dbus.WithMatchMember("PropertiesChanged"), dbus.WithMatchArg(0, deviceIfc),
			dbus.WithMatchSender(service), dbus.WithMatchPathNamespace(c.adapter)},
		{dbus.WithMatchInterface(propsIfc), dbus.WithMatchMember("PropertiesChanged"), dbus.WithMatchArg(0, gattCharIfc),
			dbus.WithMatchSender(service), dbus.WithMatchPathNamespace(c.adapter)},
		// A device can go without Connected ever turning false: hci0 set up again (the Secure
		// Connections switch does that) takes the adapter and its devices away, and bluetoothd
		// restarting takes everything.
		{dbus.WithMatchInterface(objMgrIfc), dbus.WithMatchMember("InterfacesRemoved"), dbus.WithMatchSender(service)},
		{dbus.WithMatchInterface("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged"),
			dbus.WithMatchSender("org.freedesktop.DBus"), dbus.WithMatchArg(0, service)},
	}
	for _, o := range opts {
		if err := c.conn.AddMatchSignal(o...); err != nil {
			return fmt.Errorf("bluez: match: %w", err)
		}
	}
	ch := make(chan *dbus.Signal, 64)
	c.conn.Signal(ch)
	ctx, cancel := context.WithCancel(context.Background())
	c.stop = cancel
	go func() {
		for {
			select {
			case <-ctx.Done():
				c.conn.RemoveSignal(ch)
				return
			case s := <-ch:
				if s == nil {
					return
				}
				c.signal(s)
			}
		}
	}()
	return nil
}

func (c *Central) signal(s *dbus.Signal) {
	switch s.Name {
	case objMgrIfc + ".InterfacesRemoved":
		if len(s.Body) < 2 {
			return
		}
		path, _ := s.Body[0].(dbus.ObjectPath)
		ifcs, _ := s.Body[1].([]string)
		for _, i := range ifcs {
			if i == deviceIfc || i == adapterIfc {
				c.gone(func(l *link) bool {
					return l.path == path || strings.HasPrefix(string(l.path), string(path)+"/")
				})
			}
		}
		return
	case "org.freedesktop.DBus.NameOwnerChanged":
		// bluetoothd went (or came back with nothing connected): every link went with it.
		c.gone(func(*link) bool { return true })
		return
	case propsIfc + ".PropertiesChanged":
	default:
		return
	}
	if len(s.Body) < 2 {
		return
	}
	ifc, _ := s.Body[0].(string)
	props, _ := s.Body[1].(map[string]dbus.Variant)
	switch ifc {
	case deviceIfc:
		v, ok := props["Connected"]
		if on, _ := v.Value().(bool); !ok || on {
			return
		}
		c.gone(func(l *link) bool { return l.path == s.Path })
	case gattCharIfc:
		// Only for a characteristic notifying through StartNotify: bluetoothd also changes Value
		// after a read, and a read is answered on its own.
		v, ok := props["Value"]
		if !ok {
			return
		}
		data, _ := v.Value().([]byte)
		addr, handle, ok := c.notifier(s.Path)
		if ok && c.value != nil {
			c.value(addr, handle, data)
		}
	}
}

// notifier finds which device's notifying characteristic path is.
func (c *Central) notifier(path dbus.ObjectPath) (string, uint16, bool) {
	h, ok := handleOf(path)
	if !ok {
		return "", 0, false
	}
	h = valueHandle(h)
	c.mu.Lock()
	defer c.mu.Unlock()
	for a, l := range c.links {
		if _, on := l.notifying[h]; on && strings.HasPrefix(string(path), string(l.path)+"/") {
			return a, h, true
		}
	}
	return "", 0, false
}

// gone forgets the links that match, which went without being asked to, and tells lost of each.
func (c *Central) gone(match func(*link) bool) {
	var addrs []string
	var stops []func()
	c.mu.Lock()
	for a, l := range c.links {
		if !l.closing && match(l) {
			addrs = append(addrs, a)
			stops = append(stops, c.drop(a, l)...)
		}
	}
	c.mu.Unlock()
	stopAll(stops)
	if c.lost != nil {
		for _, a := range addrs {
			c.lost(a)
		}
	}
}

// drop forgets a link and returns what stops its notifications. Called with mu held; the caller runs
// them after letting go of it, since each can be a call to bluetoothd.
func (c *Central) drop(address string, l *link) []func() {
	stops := make([]func(), 0, len(l.notifying))
	for _, stop := range l.notifying {
		stops = append(stops, stop)
	}
	delete(c.links, address)
	return stops
}

func stopAll(stops []func()) {
	for _, stop := range stops {
		stop()
	}
}

// Connected reports whether there is a link to the device.
func (c *Central) Connected(address string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.links[strings.ToUpper(address)]
	return ok
}

// Connect opens a GATT connection to an LE device and waits for bluetoothd to have read its
// services, then reports the ATT MTU. random says the address is a random one, which only matters
// for a device bluetoothd has not heard yet.
func (c *Central) Connect(ctx context.Context, address string, random bool) (int, error) {
	address = strings.ToUpper(address)
	path, err := c.devicePath(address)
	if err != nil {
		return 0, err
	}
	if path == "" {
		// Not heard yet: ConnectDevice makes the device and connects it in one call. It is
		// experimental in bluetoothd (-E) and fails without it, the same as an unknown device.
		kind := "public"
		if random {
			kind = "random"
		}
		args := map[string]dbus.Variant{"Address": dbus.MakeVariant(address), "AddressType": dbus.MakeVariant(kind)}
		if err := c.conn.Object(service, c.adapter).CallWithContext(ctx, adapterIfc+".ConnectDevice", 0, args).Store(&path); err != nil {
			return 0, fmt.Errorf("bluez: %s not found: %w", address, err)
		}
	} else {
		err := c.conn.Object(service, path).CallWithContext(ctx, deviceIfc+".Connect", 0).Err
		if err != nil && !strings.Contains(err.Error(), "AlreadyConnected") {
			return 0, fmt.Errorf("bluez: connecting %s: %w", address, err)
		}
	}
	if err := c.resolved(ctx, path); err != nil {
		_ = c.conn.Object(service, path).Call(deviceIfc+".Disconnect", 0).Err
		return 0, err
	}

	l := &link{path: path, notifying: map[uint16]func(){}}
	var stops []func()
	c.mu.Lock()
	if old, ok := c.links[address]; ok {
		stops = c.drop(address, old)
	}
	c.links[address] = l
	c.mu.Unlock()
	stopAll(stops)

	mtu := defaultMTU
	if _, err := c.Services(address); err == nil {
		// Any characteristic says the link's MTU.
		var char dbus.ObjectPath
		c.mu.Lock()
		for _, a := range l.attrs {
			if a.ifc == gattCharIfc {
				char = a.path
				break
			}
		}
		c.mu.Unlock()
		if char != "" {
			if v, err := c.conn.Object(service, char).GetProperty(gattCharIfc + ".MTU"); err == nil {
				if m, ok := v.Value().(uint16); ok && int(m) > mtu {
					mtu = int(m)
				}
			}
		}
	}
	return mtu, nil
}

// resolved waits for a connected device's services to be discovered.
func (c *Central) resolved(ctx context.Context, path dbus.ObjectPath) error {
	o := c.conn.Object(service, path)
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		connected, _ := o.GetProperty(deviceIfc + ".Connected")
		if on, _ := connected.Value().(bool); !on {
			// Connect returns once the link is up; a device that drops it straight away is gone.
			return fmt.Errorf("bluez: %s disconnected", path)
		}
		done, _ := o.GetProperty(deviceIfc + ".ServicesResolved")
		if ok, _ := done.Value().(bool); ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("bluez: waiting for services: %w", ctx.Err())
		case <-t.C:
		}
	}
}

// devicePath is where bluetoothd has the device, or "" when it doesn't know it. bluetoothd names a
// device's object after its address, so this asks about that one object rather than reading every
// device it knows, which near a busy street is hundreds.
func (c *Central) devicePath(address string) (dbus.ObjectPath, error) {
	path := dbus.ObjectPath(string(c.adapter) + "/dev_" + strings.ReplaceAll(strings.ToUpper(address), ":", "_"))
	v, err := c.conn.Object(service, path).GetProperty(deviceIfc + ".Address")
	if err != nil {
		var de dbus.Error
		if errors.As(err, &de) && strings.HasPrefix(de.Name, "org.freedesktop.DBus.Error.") {
			return "", nil // no such object, or no Device1 on it
		}
		return "", fmt.Errorf("bluez: %s: %w", address, err)
	}
	if a, _ := v.Value().(string); !strings.EqualFold(a, address) {
		return "", nil
	}
	return path, nil
}

// Known waits for bluetoothd to know the device, which it forgets a while after a connection ends
// and learns again from its next advertisement. Scanning must be on for that.
func (c *Central) Known(ctx context.Context, address string) error {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		path, err := c.devicePath(strings.ToUpper(address))
		if err != nil || path != "" {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("bluez: %s not heard: %w", address, ctx.Err())
		case <-t.C:
		}
	}
}

// Disconnect drops the link to a device.
func (c *Central) Disconnect(ctx context.Context, address string) error {
	address = strings.ToUpper(address)
	c.mu.Lock()
	l, ok := c.links[address]
	var stops []func()
	if ok {
		l.closing = true
		stops = c.drop(address, l)
	}
	c.mu.Unlock()
	if !ok {
		return nil
	}
	stopAll(stops)
	err := c.conn.Object(service, l.path).CallWithContext(ctx, deviceIfc+".Disconnect", 0).Err
	if err != nil && strings.Contains(err.Error(), "NotConnected") {
		return nil
	}
	return err
}

// Services reads a connected device's attributes, in handle order.
func (c *Central) Services(address string) ([]Service, error) {
	address = strings.ToUpper(address)
	c.mu.Lock()
	l, ok := c.links[address]
	c.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("bluez: %s not connected", address)
	}
	objs, err := c.objects()
	if err != nil {
		return nil, err
	}
	services, attrs := tree(l.path, objs)
	c.mu.Lock()
	l.attrs = attrs
	c.mu.Unlock()
	return services, nil
}

// tree builds the attribute table of the device at path from what bluetoothd exports.
func tree(path dbus.ObjectPath, objs map[dbus.ObjectPath]map[string]map[string]dbus.Variant) ([]Service, map[uint16]attribute) {
	attrs := map[uint16]attribute{}
	byService := map[dbus.ObjectPath]*Service{}
	byChar := map[dbus.ObjectPath]*Characteristic{}
	prefix := string(path) + "/"

	str := func(props map[string]dbus.Variant, k string) string {
		s, _ := props[k].Value().(string)
		return strings.ToLower(s)
	}
	// Services, then characteristics, then descriptors, so each has its parent to go under.
	for _, ifc := range []string{gattServiceIfc, gattCharIfc, gattDescIfc} {
		for p, ifcs := range objs {
			props, ok := ifcs[ifc]
			if !ok || !strings.HasPrefix(string(p), prefix) {
				continue
			}
			h, ok := handleOf(p)
			if !ok {
				continue
			}
			parent := dbus.ObjectPath(string(p)[:strings.LastIndex(string(p), "/")])
			uuid := str(props, "UUID")
			switch ifc {
			case gattServiceIfc:
				byService[p] = &Service{Handle: h, UUID: uuid}
				attrs[h] = attribute{path: p, ifc: ifc, uuid: uuid}
			case gattCharIfc:
				if _, ok := byService[parent]; !ok {
					continue
				}
				h = valueHandle(h)
				flags, _ := props["Flags"].Value().([]string)
				byChar[p] = &Characteristic{Handle: h, UUID: uuid, Flags: flags}
				attrs[h] = attribute{path: p, ifc: ifc, uuid: uuid}
			case gattDescIfc:
				ch, ok := byChar[parent]
				if !ok {
					continue
				}
				ch.Descriptors = append(ch.Descriptors, Descriptor{Handle: h, UUID: uuid})
				attrs[h] = attribute{path: p, ifc: ifc, uuid: uuid, char: ch.Handle}
			}
		}
	}
	for p, ch := range byChar {
		sort.Slice(ch.Descriptors, func(i, j int) bool { return ch.Descriptors[i].Handle < ch.Descriptors[j].Handle })
		s := byService[dbus.ObjectPath(string(p)[:strings.LastIndex(string(p), "/")])]
		s.Characteristics = append(s.Characteristics, *ch)
	}
	out := make([]Service, 0, len(byService))
	for _, s := range byService {
		sort.Slice(s.Characteristics, func(i, j int) bool { return s.Characteristics[i].Handle < s.Characteristics[j].Handle })
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Handle < out[j].Handle })
	return out, attrs
}

// valueHandle is a characteristic's value handle from its declaration's, which bluetoothd names
// it by: the value comes straight after the declaration (Core Specification, Vol 3, Part G, 3.3).
func valueHandle(declaration uint16) uint16 { return declaration + 1 }

// handleOf reads the ATT handle out of an attribute's object path: the hex after service, char or
// desc in its last element, as bluetoothd names them (.../service000c/char000d/desc000f).
func handleOf(path dbus.ObjectPath) (uint16, bool) {
	last := string(path)[strings.LastIndex(string(path), "/")+1:]
	for _, p := range []string{"service", "char", "desc"} {
		if hex, ok := strings.CutPrefix(last, p); ok && len(hex) == 4 {
			v, err := strconv.ParseUint(hex, 16, 16)
			return uint16(v), err == nil
		}
	}
	return 0, false
}

func (c *Central) attribute(address string, handle uint16) (attribute, error) {
	address = strings.ToUpper(address)
	c.mu.Lock()
	l, ok := c.links[address]
	var a attribute
	found, loaded := false, false
	if ok {
		a, found = l.attrs[handle]
		loaded = l.attrs != nil
	}
	c.mu.Unlock()
	if !ok {
		return attribute{}, fmt.Errorf("bluez: %s not connected", address)
	}
	if !found && !loaded {
		// A client with services it read on an earlier connection asks without reading them again;
		// they are read once for the connection. An unknown handle after that is the client's.
		if _, err := c.Services(address); err != nil {
			return attribute{}, err
		}
		c.mu.Lock()
		a, found = l.attrs[handle]
		c.mu.Unlock()
	}
	if !found {
		return attribute{}, fmt.Errorf("bluez: %s has no handle 0x%04x", address, handle)
	}
	return a, nil
}

// Read reads a characteristic's or a descriptor's value.
func (c *Central) Read(ctx context.Context, address string, handle uint16) ([]byte, error) {
	a, err := c.attribute(address, handle)
	if err != nil {
		return nil, err
	}
	if a.ifc == gattServiceIfc {
		return nil, fmt.Errorf("bluez: handle 0x%04x is a service", handle)
	}
	var data []byte
	err = c.conn.Object(service, a.path).CallWithContext(ctx, a.ifc+".ReadValue", 0, map[string]dbus.Variant{}).Store(&data)
	return data, err
}

// Write writes a characteristic's or a descriptor's value, waiting for the device to acknowledge it
// when response is set (a characteristic's write request rather than a command). A write to a
// characteristic's configuration descriptor turns its notifications on or off instead, since
// bluetoothd keeps that descriptor for itself.
func (c *Central) Write(ctx context.Context, address string, handle uint16, data []byte, response bool) error {
	a, err := c.attribute(address, handle)
	if err != nil {
		return err
	}
	switch a.ifc {
	case gattDescIfc:
		if a.uuid == cccd {
			return c.Notify(ctx, address, a.char, len(data) > 0 && data[0]&0x03 != 0)
		}
		return c.conn.Object(service, a.path).CallWithContext(ctx, gattDescIfc+".WriteValue", 0, data, map[string]dbus.Variant{}).Err
	case gattCharIfc:
		kind := "command"
		if response {
			kind = "request"
		}
		opts := map[string]dbus.Variant{"type": dbus.MakeVariant(kind)}
		return c.conn.Object(service, a.path).CallWithContext(ctx, gattCharIfc+".WriteValue", 0, data, opts).Err
	}
	return fmt.Errorf("bluez: handle 0x%04x is a service", handle)
}

// Notify turns a characteristic's notifications or indications on or off. What it sends arrives at
// the value callback.
//
// AcquireNotify is tried first: it hands over a socket that carries only notifications, where
// StartNotify reports them as changes to Value, which a read changes too. It is offered only for a
// characteristic that notifies rather than indicates, and only while no one else has notifications
// on it; StartNotify covers the rest.
func (c *Central) Notify(ctx context.Context, address string, handle uint16, on bool) error {
	address = strings.ToUpper(address)
	a, err := c.attribute(address, handle)
	if err != nil {
		return err
	}
	if a.ifc != gattCharIfc {
		return fmt.Errorf("bluez: handle 0x%04x is not a characteristic", handle)
	}
	c.mu.Lock()
	l, ok := c.links[address]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("bluez: %s not connected", address)
	}
	stop, running := l.notifying[handle]
	if !on && running {
		delete(l.notifying, handle)
	}
	c.mu.Unlock()
	if !on {
		if running {
			stop()
		}
		return nil
	}
	if running {
		return nil
	}

	o := c.conn.Object(service, a.path)
	var fd dbus.UnixFD
	var mtu uint16
	if err := o.CallWithContext(ctx, gattCharIfc+".AcquireNotify", 0, map[string]dbus.Variant{}).Store(&fd, &mtu); err == nil {
		// Non-blocking, so the runtime polls it and Close wakes the reader.
		if err := unix.SetNonblock(int(fd), true); err != nil {
			_ = unix.Close(int(fd))
			return fmt.Errorf("bluez: notify socket: %w", err)
		}
		f := os.NewFile(uintptr(fd), "notify")
		c.notifying(address, l, handle, func() { _ = f.Close() })
		go c.relay(address, handle, f, int(mtu))
		return nil
	} else {
		slog.Debug("bluez: AcquireNotify unavailable, using StartNotify", "handle", handle, "err", err)
	}

	// Registered before StartNotify, so a notification sent straight away is not missed.
	c.notifying(address, l, handle, func() {
		if err := o.Call(gattCharIfc+".StopNotify", 0).Err; err != nil {
			slog.Debug("bluez: StopNotify", "handle", handle, "err", err)
		}
	})
	err = o.CallWithContext(ctx, gattCharIfc+".StartNotify", 0).Err
	if err != nil && !strings.Contains(err.Error(), "InProgress") {
		c.mu.Lock()
		delete(l.notifying, handle)
		c.mu.Unlock()
		return err
	}
	return nil
}

func (c *Central) notifying(address string, l *link, handle uint16, stop func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.links[address] != l {
		// Disconnected meanwhile: nothing to keep running.
		go stop()
		return
	}
	l.notifying[handle] = stop
}

// relay passes on what arrives on an AcquireNotify socket, one notification per read, until it is
// closed.
func (c *Central) relay(address string, handle uint16, f *os.File, mtu int) {
	defer f.Close()
	if mtu < defaultMTU {
		mtu = 512
	}
	buf := make([]byte, mtu)
	for {
		n, err := f.Read(buf)
		if err != nil {
			return
		}
		if c.value != nil {
			c.value(address, handle, append([]byte(nil), buf[:n]...))
		}
	}
}
