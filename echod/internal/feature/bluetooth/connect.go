package bluetooth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"google.golang.org/protobuf/proto"

	"github.com/ygelfand/go-esphome-device/api"

	"github.com/HuskerMinion/techo5/echod/internal/lib/bluez"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// Active connections: Home Assistant connecting through the device to a BLE device's GATT server
// and reading, writing and subscribing to it, for integrations that control something rather than
// only listen to it.
//
// The device advertises remote caching, which Home Assistant requires of every proxy it connects
// through: it keeps the services it read and the MTU between connections, asks for services only when
// it has none, and after a notify request writes the configuration descriptor itself. bluetoothd
// keeps that descriptor for itself, so the write is turned into notifications on or off (see
// bluez.Central.Write). Pairing and cache clearing are not advertised.

const (
	connectTimeout = 30 * time.Second
	// knownTimeout is how long to wait for the device to be heard again before connecting anyway.
	knownTimeout = 5 * time.Second
	// connectTries is how many times to try before giving up: on the Spot the first connection
	// after scanning stops can fail in the kernel (ENOSYS from the ATT socket) and the next one work.
	connectTries = 3
	retryWait    = 500 * time.Millisecond

	// goneCheck is how often connections held for Home Assistant are checked for a closed session. The
	// first send to a socket whose other end went usually still succeeds, so a closed session is seen
	// on the second check: about ten seconds.
	goneCheck = 5 * time.Second
	opTimeout = 15 * time.Second

	// queuedOps is how many requests for one device may wait for the ones before them.
	queuedOps = 64
)

// ATT and ESP-IDF status codes, which is what Home Assistant expects in an error: it names them.
const (
	attReadNotPermitted  = 0x02
	attWriteNotPermitted = 0x03
	attNotSupported      = 0x06
	attInvalidOffset     = 0x07
	attNotAuthorized     = 0x08
	attInvalidLength     = 0x0d
	espNoResources       = 0x80
	espGattError         = 0x85
)

// central is a GATT client: bluetoothd's on the Show and the Spot.
type central interface {
	Known(ctx context.Context, address string) error
	Connect(ctx context.Context, address string, random bool) (int, error)
	Disconnect(ctx context.Context, address string) error
	Services(address string) ([]bluez.Service, error)
	Read(ctx context.Context, address string, handle uint16) ([]byte, error)
	Write(ctx context.Context, address string, handle uint16, data []byte, response bool) error
	Notify(ctx context.Context, address string, handle uint16, on bool) error
}

// sender is the Home Assistant connection a reply goes to.
type sender interface {
	Send(proto.Message) error
}

// connections lends Home Assistant the radio's connections. Requests arrive on the API
// connection's goroutine and are queued per device, so a slow device holds up only its own.
type connections struct {
	open func(value func(string, uint16, []byte), lost func(string)) (central, error)
	// pause, when set, stops scanning while a connection is made, and returns what resumes it.
	pause func() (resume func())
	// allowed, when set, says whether connections are lent at all (the proxy and its connections
	// switch on). Requests while it says no are refused, whoever sends them.
	allowed func() bool

	mu      sync.Mutex
	central central
	peers   map[uint64]*peer
	watcher sender // who asked to hear about free slots
}

// peer is one device Home Assistant has connected, or is connecting. mtu is set once it is connected.
type peer struct {
	conn sender
	work chan op
	quit chan struct{}
	mtu  int
}

// op is one request for a device: run does it and answers; fail answers with an error instead, for a
// request still queued when the device goes, so Home Assistant is not left waiting for a timeout.
type op struct {
	run  func()
	fail func()
}

// address48 is a device address as the radio has it: Home Assistant sends 64 bits, and a value with
// anything above the low 48 must not become a second peer for the same device.
func address48(a uint64) uint64 { return a & 0xFFFF_FFFF_FFFF }

func newConnections(open func(func(string, uint16, []byte), func(string)) (central, error)) *connections {
	return &connections{open: open, peers: map[uint64]*peer{}}
}

// handles reports whether msg is one of the active connection messages.
func handles(msg proto.Message) bool {
	switch msg.(type) {
	case *api.SubscribeBluetoothConnectionsFreeRequest, *api.BluetoothDeviceRequest,
		*api.BluetoothGATTGetServicesRequest, *api.BluetoothGATTReadRequest,
		*api.BluetoothGATTWriteRequest, *api.BluetoothGATTReadDescriptorRequest,
		*api.BluetoothGATTWriteDescriptorRequest, *api.BluetoothGATTNotifyRequest:
		return true
	}
	return false
}

// Handle answers one active connection message. It never waits on the radio.
func (c *connections) Handle(conn sender, msg proto.Message) error {
	if c.allowed != nil && !c.allowed() {
		return refuse(conn, msg)
	}
	switch m := msg.(type) {
	case *api.SubscribeBluetoothConnectionsFreeRequest:
		// A new Home Assistant session: devices the last one connected have nobody to answer to.
		c.mu.Lock()
		c.watcher = conn
		var stale []uint64
		for a, p := range c.peers {
			if p.conn != conn {
				stale = append(stale, a)
			}
		}
		c.mu.Unlock()
		for _, a := range stale {
			c.release(a, false)
		}
		return conn.Send(c.free())

	case *api.BluetoothDeviceRequest:
		return c.device(conn, m)

	case *api.BluetoothGATTGetServicesRequest:
		return c.queue(conn, m.GetAddress(), 0, func(ct central, addr string) error {
			services, err := ct.Services(addr)
			if err != nil {
				return err
			}
			for _, s := range services {
				if err := conn.Send(&api.BluetoothGATTGetServicesResponse{Address: m.GetAddress(), Services: []*api.BluetoothGATTService{gattService(s)}}); err != nil {
					return err
				}
			}
			return conn.Send(&api.BluetoothGATTGetServicesDoneResponse{Address: m.GetAddress()})
		})

	case *api.BluetoothGATTReadRequest:
		return c.read(conn, m.GetAddress(), m.GetHandle())

	case *api.BluetoothGATTReadDescriptorRequest:
		return c.read(conn, m.GetAddress(), m.GetHandle())

	case *api.BluetoothGATTWriteRequest:
		return c.write(conn, m.GetAddress(), m.GetHandle(), m.GetData(), m.GetResponse())

	case *api.BluetoothGATTWriteDescriptorRequest:
		return c.write(conn, m.GetAddress(), m.GetHandle(), m.GetData(), true)

	case *api.BluetoothGATTNotifyRequest:
		return c.queue(conn, m.GetAddress(), m.GetHandle(), func(ct central, addr string) error {
			ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
			defer cancel()
			if err := ct.Notify(ctx, addr, uint16(m.GetHandle()), m.GetEnable()); err != nil {
				return err
			}
			return conn.Send(&api.BluetoothGATTNotifyResponse{Address: m.GetAddress(), Handle: m.GetHandle()})
		})
	}
	return nil
}

// refuse answers a request made while connections are not lent, as a device with no free slot would.
func refuse(conn sender, msg proto.Message) error {
	switch m := msg.(type) {
	case *api.SubscribeBluetoothConnectionsFreeRequest:
		return conn.Send(&api.BluetoothConnectionsFreeResponse{})
	case *api.BluetoothDeviceRequest:
		return conn.Send(&api.BluetoothDeviceConnectionResponse{Address: address48(m.GetAddress()), Error: espNoResources})
	case interface {
		GetAddress() uint64
		GetHandle() uint32
	}:
		return conn.Send(&api.BluetoothGATTErrorResponse{Address: address48(m.GetAddress()), Handle: m.GetHandle(), Error: espGattError})
	case *api.BluetoothGATTGetServicesRequest:
		return conn.Send(&api.BluetoothGATTErrorResponse{Address: address48(m.GetAddress()), Error: espGattError})
	}
	return nil
}

func (c *connections) read(conn sender, address uint64, handle uint32) error {
	return c.queue(conn, address, handle, func(ct central, addr string) error {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		data, err := ct.Read(ctx, addr, uint16(handle))
		if err != nil {
			return readError{err}
		}
		return conn.Send(&api.BluetoothGATTReadResponse{Address: address, Handle: handle, Data: data})
	})
}

func (c *connections) write(conn sender, address uint64, handle uint32, data []byte, response bool) error {
	return c.queue(conn, address, handle, func(ct central, addr string) error {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		if err := ct.Write(ctx, addr, uint16(handle), data, response); err != nil {
			return err
		}
		return conn.Send(&api.BluetoothGATTWriteResponse{Address: address, Handle: handle})
	})
}

// device connects or disconnects one.
func (c *connections) device(conn sender, m *api.BluetoothDeviceRequest) error {
	address := address48(m.GetAddress())
	switch m.GetRequestType() {
	case api.BluetoothDeviceRequestType_BLUETOOTH_DEVICE_REQUEST_TYPE_CONNECT,
		api.BluetoothDeviceRequestType_BLUETOOTH_DEVICE_REQUEST_TYPE_CONNECT_V3_WITH_CACHE,
		api.BluetoothDeviceRequestType_BLUETOOTH_DEVICE_REQUEST_TYPE_CONNECT_V3_WITHOUT_CACHE:
	case api.BluetoothDeviceRequestType_BLUETOOTH_DEVICE_REQUEST_TYPE_DISCONNECT:
		c.mu.Lock()
		_, ok := c.peers[address]
		c.mu.Unlock()
		if !ok {
			// Home Assistant waits for this whether or not there was a connection.
			return conn.Send(&api.BluetoothDeviceConnectionResponse{Address: address})
		}
		c.release(address, true)
		return nil
	default:
		// Pairing and the cache are not advertised, so Home Assistant does not ask for them.
		slog.Debug("ble: device request not supported", "type", m.GetRequestType())
		return nil
	}

	c.mu.Lock()
	if p, ok := c.peers[address]; ok {
		// Asked again while connecting or connected, which Home Assistant does when two of its
		// clients reach for the same device: replies go by address, so an error here would fail the
		// first one too. The connect in flight answers both; a finished one is repeated.
		mtu := p.mtu
		c.mu.Unlock()
		if mtu == 0 {
			return nil
		}
		return conn.Send(&api.BluetoothDeviceConnectionResponse{Address: address, Connected: true, Mtu: uint32(mtu)})
	}
	if len(c.peers) >= slots {
		c.mu.Unlock()
		return conn.Send(&api.BluetoothDeviceConnectionResponse{Address: address, Error: espNoResources})
	}
	ct, err := c.centralLocked()
	if err != nil {
		c.mu.Unlock()
		slog.Warn("ble: no gatt client", "err", err)
		return conn.Send(&api.BluetoothDeviceConnectionResponse{Address: address, Error: espGattError})
	}
	p := &peer{conn: conn, work: make(chan op, queuedOps), quit: make(chan struct{})}
	c.peers[address] = p
	c.mu.Unlock()
	c.announce()

	safe.Go("ble link", func() { p.run() })
	random := m.GetHasAddressType() && m.GetAddressType() == 1
	p.work <- op{fail: func() {
		_ = conn.Send(&api.BluetoothDeviceConnectionResponse{Address: address, Error: espGattError})
	}, run: func() {
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()
		addr := addressString(address)
		mtu, err := c.connect(ctx, ct, addr, random)
		if err != nil {
			slog.Info("ble: connect failed", "address", addr, "err", err)
			c.forget(address, p, &api.BluetoothDeviceConnectionResponse{Address: address, Error: espGattError})
			return
		}
		c.mu.Lock()
		p.mtu = mtu
		c.mu.Unlock()
		slog.Info("ble: connected", "address", addr, "mtu", mtu)
		if err := conn.Send(&api.BluetoothDeviceConnectionResponse{Address: address, Connected: true, Mtu: uint32(mtu)}); err != nil {
			c.release(address, false)
		}
	}}
	return nil
}

// connect makes the connection, retrying while there is time: it waits for the device to be known
// with scanning on, then pauses scanning for the attempt itself.
func (c *connections) connect(ctx context.Context, ct central, addr string, random bool) (int, error) {
	var err error
	for try := 1; try <= connectTries; try++ {
		known, cancel := context.WithTimeout(ctx, knownTimeout)
		if kerr := ct.Known(known, addr); kerr != nil {
			slog.Debug("ble: device not heard yet", "address", addr, "err", kerr)
		}
		cancel()

		var resume func()
		if c.pause != nil {
			resume = c.pause()
		}
		var mtu int
		mtu, err = ct.Connect(ctx, addr, random)
		if resume != nil {
			resume()
		}
		if err == nil {
			return mtu, nil
		}
		slog.Debug("ble: connect attempt failed", "address", addr, "try", try, "err", err)
		select {
		case <-ctx.Done():
			return 0, err
		case <-time.After(retryWait):
		}
	}
	return 0, err
}

// queue runs op on the device's worker, answering with an error if it fails.
func (c *connections) queue(conn sender, address uint64, handle uint32, do func(central, string) error) error {
	address = address48(address)
	fail := func() {
		_ = conn.Send(&api.BluetoothGATTErrorResponse{Address: address, Handle: handle, Error: espGattError})
	}
	// Queued under the lock forget takes the peer away under: either it is queued while the peer is still
	// there, and forget's drain answers it, or the peer is gone and it is answered here. Never left in a
	// queue nobody reads.
	c.mu.Lock()
	p, ok := c.peers[address]
	ct := c.central
	queued, full := false, false
	if ok && ct != nil {
		select {
		case p.work <- op{fail: fail, run: func() {
			if err := do(ct, addressString(address)); err != nil {
				slog.Debug("ble: gatt request failed", "address", addressString(address), "handle", handle, "err", err)
				_ = conn.Send(&api.BluetoothGATTErrorResponse{Address: address, Handle: handle, Error: gattError(err)})
			}
		}}:
			queued = true
		default:
			full = true
		}
	}
	c.mu.Unlock()
	switch {
	case queued:
		return nil
	case full:
		return conn.Send(&api.BluetoothGATTErrorResponse{Address: address, Handle: handle, Error: espNoResources})
	}
	return conn.Send(&api.BluetoothGATTErrorResponse{Address: address, Handle: handle, Error: espGattError})
}

func (p *peer) run() {
	for {
		select {
		case <-p.quit:
			return
		case o := <-p.work:
			o.run()
		}
	}
}

// release disconnects a device and gives up its slot, telling Home Assistant when tell is set.
func (c *connections) release(address uint64, tell bool) {
	c.mu.Lock()
	p, ok := c.peers[address]
	ct := c.central
	c.mu.Unlock()
	if !ok {
		return
	}
	// Behind whatever the device's worker is already doing, so a connect in flight finishes first.
	disconnect := func() {
		if ct != nil {
			ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
			if err := ct.Disconnect(ctx, addressString(address)); err != nil {
				slog.Debug("ble: disconnect", "address", addressString(address), "err", err)
			}
			cancel()
		}
		var notice proto.Message
		if tell {
			notice = &api.BluetoothDeviceConnectionResponse{Address: address}
		}
		c.forget(address, p, notice)
	}
	// Already gone by the time it would run: what forgot the device answered for it.
	select {
	case p.work <- op{run: disconnect, fail: func() {}}:
	default:
		safe.Go("ble release", disconnect)
	}
}

// ReleaseAll disconnects every device, for the proxy being switched off.
func (c *connections) ReleaseAll() {
	c.mu.Lock()
	var all []uint64
	for a := range c.peers {
		all = append(all, a)
	}
	c.mu.Unlock()
	for _, a := range all {
		c.release(a, true)
	}
}

// forget drops a peer and stops its worker, if it is still the one connected. notice, when set, goes
// to Home Assistant before the free slots do: told of a slot freed for a device it still thinks is
// connected, it takes the connection for stale.
func (c *connections) forget(address uint64, p *peer, notice proto.Message) {
	c.mu.Lock()
	if c.peers[address] != p {
		c.mu.Unlock()
		return
	}
	delete(c.peers, address)
	close(p.quit)
	c.mu.Unlock()
	if notice != nil {
		_ = p.conn.Send(notice)
	}
	// What was still queued is answered with an error at once.
	for {
		select {
		case o := <-p.work:
			o.fail()
			continue
		default:
		}
		break
	}
	c.announce()
}

// ReleaseGone releases the devices of every Home Assistant connection that has closed, as ESPHome
// does: the link and the slot otherwise stay taken until a new session asks for free slots, and many
// BLE devices take one connection only, so a phone app could not reach a lock meanwhile. The library
// says nothing when a connection closes; a send to it fails, and the free slots are a harmless thing
// to send.
func (c *connections) ReleaseGone() {
	c.mu.Lock()
	byConn := map[sender][]uint64{}
	for a, p := range c.peers {
		byConn[p.conn] = append(byConn[p.conn], a)
	}
	c.mu.Unlock()
	for conn, addrs := range byConn {
		if conn.Send(c.free()) == nil {
			continue
		}
		for _, a := range addrs {
			c.release(a, false)
		}
	}
}

// watch looks for closed Home Assistant connections every so often, behind OnSubscribed, which
// notices only while advertisements flow.
func (c *connections) watch() {
	t := time.NewTicker(goneCheck)
	defer t.Stop()
	for range t.C {
		c.ReleaseGone()
	}
}

// value passes a notification on to whoever connected the device.
func (c *connections) value(address string, handle uint16, data []byte) {
	a, ok := addressValue(address)
	if !ok {
		return
	}
	c.mu.Lock()
	p, ok := c.peers[a]
	c.mu.Unlock()
	if !ok {
		return
	}
	if err := p.conn.Send(&api.BluetoothGATTNotifyDataResponse{Address: a, Handle: uint32(handle), Data: data}); err != nil {
		c.release(a, false)
	}
}

// lost tells Home Assistant a device dropped the connection itself.
func (c *connections) lost(address string) {
	a, ok := addressValue(address)
	if !ok {
		return
	}
	c.mu.Lock()
	p, ok := c.peers[a]
	c.mu.Unlock()
	if !ok {
		return
	}
	slog.Info("ble: device disconnected", "address", address)
	c.forget(a, p, &api.BluetoothDeviceConnectionResponse{Address: a})
}

// centralLocked opens the GATT client the first time it is needed: bluetoothd may not be up when
// the daemon starts. Called with mu held.
func (c *connections) centralLocked() (central, error) {
	if c.central != nil {
		return c.central, nil
	}
	if c.open == nil {
		return nil, errors.New("not available on this device")
	}
	ct, err := c.open(c.value, c.lost)
	if err != nil {
		return nil, err
	}
	c.central = ct
	return ct, nil
}

func (c *connections) free() *api.BluetoothConnectionsFreeResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := &api.BluetoothConnectionsFreeResponse{Free: uint32(slots - len(c.peers)), Limit: slots}
	for a := range c.peers {
		r.Allocated = append(r.Allocated, a)
	}
	return r
}

// announce tells Home Assistant how many slots are free, which it checks before connecting.
func (c *connections) announce() {
	c.mu.Lock()
	w := c.watcher
	c.mu.Unlock()
	if w != nil {
		_ = w.Send(c.free())
	}
}

// gattService is a service as Home Assistant wants it, every UUID in its 128-bit form.
func gattService(s bluez.Service) *api.BluetoothGATTService {
	out := &api.BluetoothGATTService{Uuid: splitUUID(s.UUID), Handle: uint32(s.Handle)}
	for _, ch := range s.Characteristics {
		gc := &api.BluetoothGATTCharacteristic{Uuid: splitUUID(ch.UUID), Handle: uint32(ch.Handle), Properties: properties(ch.Flags)}
		for _, d := range ch.Descriptors {
			gc.Descriptors = append(gc.Descriptors, &api.BluetoothGATTDescriptor{Uuid: splitUUID(d.UUID), Handle: uint32(d.Handle)})
		}
		out.Characteristics = append(out.Characteristics, gc)
	}
	return out
}

// properties turns bluetoothd's flag names into the characteristic properties byte.
func properties(flags []string) uint32 {
	var p uint32
	for _, f := range flags {
		switch f {
		case "broadcast":
			p |= 0x01
		case "read":
			p |= 0x02
		case "write-without-response":
			p |= 0x04
		case "write":
			p |= 0x08
		case "notify":
			p |= 0x10
		case "indicate":
			p |= 0x20
		case "authenticated-signed-writes":
			p |= 0x40
		case "extended-properties":
			p |= 0x80
		}
	}
	return p
}

// splitUUID is a UUID as its high and low 64 bits.
func splitUUID(u string) []uint64 {
	h := strings.ReplaceAll(u, "-", "")
	if len(h) != 32 {
		return []uint64{0, 0}
	}
	hi, err1 := strconv.ParseUint(h[:16], 16, 64)
	lo, err2 := strconv.ParseUint(h[16:], 16, 64)
	if err1 != nil || err2 != nil {
		return []uint64{0, 0}
	}
	return []uint64{hi, lo}
}

// addressString is a 48-bit address as bluetoothd writes it.
func addressString(a uint64) string {
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X",
		byte(a>>40), byte(a>>32), byte(a>>24), byte(a>>16), byte(a>>8), byte(a))
}

func addressValue(s string) (uint64, bool) {
	h := strings.ReplaceAll(s, ":", "")
	if len(h) != 12 {
		return 0, false
	}
	v, err := strconv.ParseUint(h, 16, 64)
	return v, err == nil
}

// readError marks a failed read, whose permission error differs from a write's.
type readError struct{ error }

func (e readError) Unwrap() error { return e.error }

// gattError is the status for a failed request: the ATT error the device answered with when
// bluetoothd says, else ESP-IDF's generic GATT error.
func gattError(err error) int32 {
	var native interface{ GATTStatus() int32 }
	if errors.As(err, &native) {
		return native.GATTStatus()
	}
	var r readError
	read := errors.As(err, &r)
	var de dbus.Error
	if !errors.As(err, &de) {
		var dp *dbus.Error
		if !errors.As(err, &dp) {
			return espGattError
		}
		de = *dp
	}
	switch de.Name {
	case "org.bluez.Error.NotPermitted":
		if read {
			return attReadNotPermitted
		}
		return attWriteNotPermitted
	case "org.bluez.Error.NotAuthorized":
		return attNotAuthorized
	case "org.bluez.Error.NotSupported":
		return attNotSupported
	case "org.bluez.Error.InvalidOffset":
		return attInvalidOffset
	case "org.bluez.Error.InvalidValueLength":
		return attInvalidLength
	}
	// "Operation failed with ATT error: 0x0e"
	if len(de.Body) > 0 {
		if s, ok := de.Body[0].(string); ok {
			if _, code, ok := strings.Cut(s, "ATT error: 0x"); ok {
				if v, err := strconv.ParseUint(strings.TrimSpace(code), 16, 8); err == nil {
					return int32(v)
				}
			}
		}
	}
	return espGattError
}
