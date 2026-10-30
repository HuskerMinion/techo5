//go:build linux

package bluez

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"
)

const (
	gattMgrIfc   = "org.bluez.GattManager1"
	advertMgrIfc = "org.bluez.LEAdvertisingManager1"
	advertIfc    = "org.bluez.LEAdvertisement1"
)

// CharSpec is one characteristic a Peripheral serves. Flags are bluetoothd's names for its
// properties ("read", "write", "write-without-response", "notify"). Value is its first value; Write,
// when set, hears every write, on the bus's goroutine, and must not block.
type CharSpec struct {
	UUID  string
	Flags []string
	Value []byte
	Write func(value []byte)
}

// AdvertSpec is what the Peripheral advertises while it is up: the service UUIDs and service data a
// central finds it by. LocalName goes to the scan response where bluetoothd has room for it.
type AdvertSpec struct {
	ServiceUUIDs []string
	ServiceData  map[string][]byte
	LocalName    string
}

// Peripheral is one GATT service of this device's own, with the advertisement that leads to it: the
// device as the thing a phone connects to, rather than the one connecting. It has its own bus
// connection, so its objects and Adapter's or Central's do not mix.
type Peripheral struct {
	conn    *dbus.Conn
	adapter dbus.ObjectPath
	root    dbus.ObjectPath
	advert  dbus.ObjectPath
	// advertising is bluetoothd advertising the service; without it, someone else has to (Advertising).
	advertising bool

	mu      sync.Mutex
	objects map[dbus.ObjectPath]map[string]map[string]*prop.Prop
	chars   map[string]*charObj // by lower-case UUID
	props   map[dbus.ObjectPath]*prop.Properties
	closed  bool
}

type charObj struct {
	p     *Peripheral
	path  dbus.ObjectPath
	write func([]byte)
}

// OpenPeripheral connects to bluetoothd, exports the service under root (a path of this process's
// own, such as /org/techo5/improv) and registers it and its advertisement with the first adapter,
// turning the adapter on if it is off. Close takes it all down again.
//
// An adapter that can serve GATT but not advertise (a kernel before Add Advertising, such as 3.18)
// still gets the service: Advertising then reports false, and the caller advertises it some other
// way or closes it.
func OpenPeripheral(root dbus.ObjectPath, serviceUUID string, chars []CharSpec, ad AdvertSpec) (*Peripheral, error) {
	conn, err := SystemBus()
	if err != nil {
		return nil, fmt.Errorf("system bus: %w", err)
	}
	p := &Peripheral{
		conn:    conn,
		root:    root,
		advert:  root + "/advert",
		objects: map[dbus.ObjectPath]map[string]map[string]*prop.Prop{},
		chars:   map[string]*charObj{},
		props:   map[dbus.ObjectPath]*prop.Properties{},
	}
	if err := p.findAdapter(); err != nil {
		conn.Close()
		return nil, err
	}
	if err := p.export(serviceUUID, chars, ad); err != nil {
		conn.Close()
		return nil, err
	}
	adapter := conn.Object(service, p.adapter)
	_ = adapter.Call(propsIfc+".Set", 0, adapterIfc, "Powered", dbus.MakeVariant(true)).Err
	if err := adapter.Call(gattMgrIfc+".RegisterApplication", 0, root, map[string]dbus.Variant{}).Err; err != nil {
		conn.Close()
		return nil, fmt.Errorf("bluez: register application: %w", err)
	}
	if !p.advertising {
		return p, nil
	}
	if err := adapter.Call(advertMgrIfc+".RegisterAdvertisement", 0, p.advert, map[string]dbus.Variant{}).Err; err != nil {
		_ = adapter.Call(gattMgrIfc+".UnregisterApplication", 0, root).Err
		conn.Close()
		return nil, fmt.Errorf("bluez: register advertisement: %w", err)
	}
	return p, nil
}

// Advertising reports whether bluetoothd advertises the service. bluetoothd only offers
// LEAdvertisingManager1 when the kernel's management interface has the Add Advertising commands, which
// came after 3.18.
func (p *Peripheral) Advertising() bool { return p.advertising }

func (p *Peripheral) findAdapter() error {
	var objs map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	if err := p.conn.Object(service, "/").Call(objMgrIfc+".GetManagedObjects", 0).Store(&objs); err != nil {
		return fmt.Errorf("bluez: managed objects: %w", err)
	}
	adapter, advertising, err := pickAdapter(objs)
	p.adapter, p.advertising = adapter, advertising
	return err
}

// pickAdapter is the first adapter, by path, that can serve GATT and advertise; failing that, the
// first that can serve GATT, which advertising reports.
func pickAdapter(objs map[dbus.ObjectPath]map[string]map[string]dbus.Variant) (adapter dbus.ObjectPath, advertising bool, err error) {
	var gattOnly dbus.ObjectPath
	for path, ifcs := range objs {
		_, isAdapter := ifcs[adapterIfc]
		_, canGATT := ifcs[gattMgrIfc]
		_, canAdvertise := ifcs[advertMgrIfc]
		if !isAdapter || !canGATT {
			continue
		}
		if canAdvertise && (adapter == "" || path < adapter) {
			adapter = path
		}
		if !canAdvertise && (gattOnly == "" || path < gattOnly) {
			gattOnly = path
		}
	}
	switch {
	case adapter != "":
		return adapter, true, nil
	case gattOnly != "":
		return gattOnly, false, nil
	}
	return "", false, errors.New("bluez: no adapter that can serve GATT (LE off, or BlueZ too old)")
}

// export puts the service, its characteristics and the advertisement on the bus. bluetoothd finds the
// first three through GetManagedObjects on root, and the advertisement by its path.
func (p *Peripheral) export(serviceUUID string, chars []CharSpec, ad AdvertSpec) error {
	svc := p.root + "/service0"
	p.objects[svc] = map[string]map[string]*prop.Prop{gattServiceIfc: {
		"UUID":    ro(serviceUUID),
		"Primary": ro(true),
	}}
	for i, c := range chars {
		path := dbus.ObjectPath(fmt.Sprintf("%s/char%d", svc, i))
		value := c.Value
		if value == nil {
			value = []byte{}
		}
		p.objects[path] = map[string]map[string]*prop.Prop{gattCharIfc: {
			"UUID":    ro(c.UUID),
			"Service": ro(svc),
			"Flags":   ro(c.Flags),
			// Emit, so that setting it is the notification a subscribed central gets.
			"Value": {Value: value, Emit: prop.EmitTrue},
		}}
		obj := &charObj{p: p, path: path, write: c.Write}
		p.chars[strings.ToLower(c.UUID)] = obj
		if err := p.conn.Export(obj, path, gattCharIfc); err != nil {
			return err
		}
	}

	serviceData := map[string]dbus.Variant{}
	for k, v := range ad.ServiceData {
		serviceData[k] = dbus.MakeVariant(v)
	}
	advertProps := map[string]*prop.Prop{
		"Type":         ro("peripheral"),
		"ServiceUUIDs": ro(ad.ServiceUUIDs),
		"ServiceData":  ro(serviceData),
	}
	if ad.LocalName != "" {
		advertProps["LocalName"] = ro(ad.LocalName)
	}

	for path, ifcs := range p.objects {
		props, err := prop.Export(p.conn, path, toMap(ifcs))
		if err != nil {
			return err
		}
		p.props[path] = props
	}
	advProps, err := prop.Export(p.conn, p.advert, prop.Map{advertIfc: advertProps})
	if err != nil {
		return err
	}
	p.props[p.advert] = advProps
	if err := p.conn.Export(advert{}, p.advert, advertIfc); err != nil {
		return err
	}
	return p.conn.Export(manager{p}, p.root, objMgrIfc)
}

// Set changes a characteristic's value, which reaches a central that asked to be notified of it.
func (p *Peripheral) Set(uuid string, value []byte) {
	p.mu.Lock()
	c, ok := p.chars[strings.ToLower(uuid)]
	closed := p.closed
	p.mu.Unlock()
	if !ok || closed {
		return
	}
	p.props[c.path].SetMust(gattCharIfc, "Value", append([]byte(nil), value...))
}

// Close unregisters the advertisement and the service, and lets the bus go.
func (p *Peripheral) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()
	adapter := p.conn.Object(service, p.adapter)
	if p.advertising {
		_ = adapter.Call(advertMgrIfc+".UnregisterAdvertisement", 0, p.advert).Err
	}
	_ = adapter.Call(gattMgrIfc+".UnregisterApplication", 0, p.root).Err
	return p.conn.Close()
}

// value is the characteristic's value now, as its Value property holds it.
func (c *charObj) value() []byte {
	v, err := c.p.props[c.path].Get(gattCharIfc, "Value")
	if err != nil {
		return nil
	}
	b, _ := v.Value().([]byte)
	return b
}

// ReadValue is bluetoothd asking for the value on a central's behalf, from the offset it asks for:
// a value longer than the link's MTU is read in pieces.
func (c *charObj) ReadValue(options map[string]dbus.Variant) ([]byte, *dbus.Error) {
	b := c.value()
	if v, ok := options["offset"]; ok {
		if off, ok := v.Value().(uint16); ok {
			if int(off) > len(b) {
				return nil, dbus.NewError("org.bluez.Error.InvalidOffset", nil)
			}
			b = b[off:]
		}
	}
	return b, nil
}

// WriteValue is a central's write.
func (c *charObj) WriteValue(value []byte, options map[string]dbus.Variant) *dbus.Error {
	if c.write == nil {
		return dbus.NewError("org.bluez.Error.NotPermitted", nil)
	}
	c.write(append([]byte(nil), value...))
	return nil
}

// StartNotify and StopNotify need nothing done: bluetoothd forwards a Value change to whichever
// centrals have notifications on.
func (c *charObj) StartNotify() *dbus.Error { return nil }
func (c *charObj) StopNotify() *dbus.Error  { return nil }

// manager is the ObjectManager on the root, which is how bluetoothd reads the service.
type manager struct{ p *Peripheral }

func (m manager) GetManagedObjects() (map[dbus.ObjectPath]map[string]map[string]dbus.Variant, *dbus.Error) {
	out := map[dbus.ObjectPath]map[string]map[string]dbus.Variant{}
	for path, ifcs := range m.p.objects {
		out[path] = map[string]map[string]dbus.Variant{}
		for ifc := range ifcs {
			all, err := m.p.props[path].GetAll(ifc)
			if err != nil {
				return nil, err
			}
			out[path][ifc] = all
		}
	}
	return out, nil
}

// advert is the advertisement object's one method: bluetoothd letting go of it on its own.
type advert struct{}

func (advert) Release() *dbus.Error { return nil }

func ro(v any) *prop.Prop { return &prop.Prop{Value: v, Emit: prop.EmitFalse} }

func toMap(ifcs map[string]map[string]*prop.Prop) prop.Map {
	m := prop.Map{}
	for k, v := range ifcs {
		m[k] = v
	}
	return m
}
