package bluetooth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"google.golang.org/protobuf/proto"

	"github.com/ygelfand/go-esphome-device/api"

	"github.com/HuskerMinion/techo5/echod/internal/lib/bluez"
)

const testAddr = uint64(0xC0FFEE123456)

type fakeCentral struct {
	mu        sync.Mutex
	connected map[string]bool
	writes    []string
	failWrite error
	onConnect func()
	failFirst int
	readGate  chan struct{}
	value     func(string, uint16, []byte)
	lost      func(string)
}

func (f *fakeCentral) Connect(_ context.Context, a string, _ bool) (int, error) {
	if f.onConnect != nil {
		f.onConnect()
	}
	if f.failFirst > 0 {
		f.failFirst--
		return 0, errors.New("le-connection-abort-by-local")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected[a] = true
	return 247, nil
}

func (f *fakeCentral) Known(context.Context, string) error { return nil }

func (f *fakeCentral) Disconnect(_ context.Context, a string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.connected, a)
	return nil
}

func (f *fakeCentral) Services(string) ([]bluez.Service, error) {
	return []bluez.Service{{
		Handle: 0x000c,
		UUID:   "f00dcafe-0000-4000-8000-000000000001",
		Characteristics: []bluez.Characteristic{{
			Handle:      0x000d,
			UUID:        "f00dcafe-0000-4000-8000-000000000002",
			Flags:       []string{"read", "write", "notify"},
			Descriptors: []bluez.Descriptor{{Handle: 0x000f, UUID: "00002902-0000-1000-8000-00805f9b34fb"}},
		}},
	}}, nil
}

func (f *fakeCentral) Read(context.Context, string, uint16) ([]byte, error) {
	if f.readGate != nil {
		<-f.readGate
	}
	return []byte{1, 2, 3}, nil
}

func (f *fakeCentral) Write(_ context.Context, a string, _ uint16, data []byte, _ bool) error {
	if f.failWrite != nil {
		return f.failWrite
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, a+":"+string(data))
	return nil
}

func (f *fakeCentral) Notify(context.Context, string, uint16, bool) error { return nil }

type fakeConn struct {
	got  chan proto.Message
	dead bool
}

func (c *fakeConn) Send(m proto.Message) error {
	if c.dead {
		return errors.New("closed")
	}
	c.got <- m
	return nil
}

func newFakes() (*connections, *fakeCentral, *fakeConn) {
	f := &fakeCentral{connected: map[string]bool{}}
	c := newConnections(func(v func(string, uint16, []byte), l func(string)) (central, error) {
		f.value, f.lost = v, l
		return f, nil
	})
	return c, f, &fakeConn{got: make(chan proto.Message, 64)}
}

// next waits for the next reply of type T, skipping the free-slot reports in between.
func next[T proto.Message](t *testing.T, c *fakeConn) T {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case m := <-c.got:
			if v, ok := m.(T); ok {
				return v
			}
		case <-timeout:
			var zero T
			t.Fatalf("no %T", zero)
			return zero
		}
	}
}

func connect(t *testing.T, c *connections, conn *fakeConn, addr uint64) *api.BluetoothDeviceConnectionResponse {
	t.Helper()
	if err := c.Handle(conn, &api.BluetoothDeviceRequest{Address: addr, RequestType: api.BluetoothDeviceRequestType_BLUETOOTH_DEVICE_REQUEST_TYPE_CONNECT, HasAddressType: true}); err != nil {
		t.Fatal(err)
	}
	return next[*api.BluetoothDeviceConnectionResponse](t, conn)
}

func TestConnectReportsSlotsAndMTU(t *testing.T) {
	c, f, conn := newFakes()
	if err := c.Handle(conn, &api.SubscribeBluetoothConnectionsFreeRequest{}); err != nil {
		t.Fatal(err)
	}
	if free := next[*api.BluetoothConnectionsFreeResponse](t, conn); free.Free != slots || free.Limit != slots {
		t.Fatalf("free %d of %d, want %d of %d", free.Free, free.Limit, slots, slots)
	}

	r := connect(t, c, conn, testAddr)
	if !r.Connected || r.Mtu != 247 || r.Address != testAddr {
		t.Fatalf("connect response %v", r)
	}
	if !f.connected["C0:FF:EE:12:34:56"] {
		t.Fatalf("central not asked for the address: %v", f.connected)
	}
	if free := c.free(); free.Free != slots-1 || len(free.Allocated) != 1 || free.Allocated[0] != testAddr {
		t.Fatalf("free after connect %v", free)
	}
}

func TestServicesInHomeAssistantsForm(t *testing.T) {
	c, _, conn := newFakes()
	connect(t, c, conn, testAddr)
	_ = c.Handle(conn, &api.BluetoothGATTGetServicesRequest{Address: testAddr})

	s := next[*api.BluetoothGATTGetServicesResponse](t, conn)
	if len(s.Services) != 1 {
		t.Fatalf("services %v", s.Services)
	}
	svc := s.Services[0]
	if svc.Handle != 0x0c || svc.Uuid[0] != 0xf00dcafe00004000 || svc.Uuid[1] != 0x8000000000000001 {
		t.Fatalf("service %v", svc)
	}
	ch := svc.Characteristics[0]
	if ch.Handle != 0x0d || ch.Properties != 0x02|0x08|0x10 || len(ch.Descriptors) != 1 || ch.Descriptors[0].Handle != 0x0f {
		t.Fatalf("characteristic %v", ch)
	}
	next[*api.BluetoothGATTGetServicesDoneResponse](t, conn)
}

func TestReadWriteNotify(t *testing.T) {
	c, f, conn := newFakes()
	connect(t, c, conn, testAddr)

	_ = c.Handle(conn, &api.BluetoothGATTReadRequest{Address: testAddr, Handle: 0x0d})
	if r := next[*api.BluetoothGATTReadResponse](t, conn); r.Handle != 0x0d || string(r.Data) != "\x01\x02\x03" {
		t.Fatalf("read %v", r)
	}

	_ = c.Handle(conn, &api.BluetoothGATTWriteRequest{Address: testAddr, Handle: 0x0d, Data: []byte("on"), Response: true})
	next[*api.BluetoothGATTWriteResponse](t, conn)
	if len(f.writes) != 1 || f.writes[0] != "C0:FF:EE:12:34:56:on" {
		t.Fatalf("writes %v", f.writes)
	}

	_ = c.Handle(conn, &api.BluetoothGATTNotifyRequest{Address: testAddr, Handle: 0x0d, Enable: true})
	next[*api.BluetoothGATTNotifyResponse](t, conn)
	f.value("C0:FF:EE:12:34:56", 0x0d, []byte{9})
	if n := next[*api.BluetoothGATTNotifyDataResponse](t, conn); n.Address != testAddr || n.Handle != 0x0d || n.Data[0] != 9 {
		t.Fatalf("notify data %v", n)
	}
}

func TestWriteErrorCarriesATTStatus(t *testing.T) {
	c, f, conn := newFakes()
	connect(t, c, conn, testAddr)
	f.failWrite = dbus.Error{Name: "org.bluez.Error.Failed", Body: []any{"Operation failed with ATT error: 0x0e"}}

	_ = c.Handle(conn, &api.BluetoothGATTWriteRequest{Address: testAddr, Handle: 0x0d, Data: []byte{1}})
	if e := next[*api.BluetoothGATTErrorResponse](t, conn); e.Handle != 0x0d || e.Error != 0x0e {
		t.Fatalf("error %v", e)
	}
}

func TestRequestWithoutConnectionFails(t *testing.T) {
	c, _, conn := newFakes()
	_ = c.Handle(conn, &api.BluetoothGATTReadRequest{Address: testAddr, Handle: 3})
	if e := next[*api.BluetoothGATTErrorResponse](t, conn); e.Handle != 3 || e.Error == 0 {
		t.Fatalf("error %v", e)
	}
}

func TestDeviceDroppingFreesTheSlot(t *testing.T) {
	c, f, conn := newFakes()
	connect(t, c, conn, testAddr)
	f.lost("C0:FF:EE:12:34:56")
	if r := next[*api.BluetoothDeviceConnectionResponse](t, conn); r.Connected {
		t.Fatalf("still connected: %v", r)
	}
	if free := c.free(); free.Free != slots {
		t.Fatalf("free %d after the device went", free.Free)
	}
}

func TestDisconnect(t *testing.T) {
	c, f, conn := newFakes()
	connect(t, c, conn, testAddr)
	_ = c.Handle(conn, &api.BluetoothDeviceRequest{Address: testAddr, RequestType: api.BluetoothDeviceRequestType_BLUETOOTH_DEVICE_REQUEST_TYPE_DISCONNECT})
	if r := next[*api.BluetoothDeviceConnectionResponse](t, conn); r.Connected {
		t.Fatalf("still connected: %v", r)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.connected) != 0 {
		t.Fatalf("central still connected: %v", f.connected)
	}
}

func TestNoSlotLeft(t *testing.T) {
	c, _, conn := newFakes()
	for i := range slots {
		connect(t, c, conn, testAddr+uint64(i))
	}
	if r := connect(t, c, conn, testAddr+slots); r.Connected || r.Error != espNoResources {
		t.Fatalf("connect past the limit: %v", r)
	}
}

func TestNewSessionReleasesTheOldOnesDevices(t *testing.T) {
	c, f, old := newFakes()
	connect(t, c, old, testAddr)

	fresh := &fakeConn{got: make(chan proto.Message, 64)}
	_ = c.Handle(fresh, &api.SubscribeBluetoothConnectionsFreeRequest{})
	deadline := time.Now().Add(2 * time.Second)
	for c.free().Free != slots && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if c.free().Free != slots {
		t.Fatal("the old session's device kept its slot")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.connected) != 0 {
		t.Fatalf("central still connected: %v", f.connected)
	}
}

func TestGattErrorNames(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int32
	}{
		{dbus.Error{Name: "org.bluez.Error.NotPermitted"}, attWriteNotPermitted},
		{readError{dbus.Error{Name: "org.bluez.Error.NotPermitted"}}, attReadNotPermitted},
		{&dbus.Error{Name: "org.bluez.Error.NotAuthorized"}, attNotAuthorized},
		{errors.New("anything else"), espGattError},
	} {
		if got := gattError(tc.err); got != tc.want {
			t.Errorf("gattError(%v) = %#x, want %#x", tc.err, got, tc.want)
		}
	}
}

func TestAddresses(t *testing.T) {
	if s := addressString(testAddr); s != "C0:FF:EE:12:34:56" {
		t.Fatal(s)
	}
	if v, ok := addressValue("c0:ff:ee:12:34:56"); !ok || v != testAddr {
		t.Fatal(v, ok)
	}
}

func TestScanningPausesWhileConnecting(t *testing.T) {
	c, f, conn := newFakes()
	var mu sync.Mutex
	paused, during := 0, false
	c.pause = func() func() {
		mu.Lock()
		paused++
		mu.Unlock()
		return func() {
			mu.Lock()
			paused--
			mu.Unlock()
		}
	}
	f.onConnect = func() {
		mu.Lock()
		during = paused == 1
		mu.Unlock()
	}
	connect(t, c, conn, testAddr)
	mu.Lock()
	defer mu.Unlock()
	if !during || paused != 0 {
		t.Fatalf("paused during connect %v, still paused %d", during, paused)
	}
}

func TestDisconnectIsToldBeforeTheSlotIsFree(t *testing.T) {
	c, f, conn := newFakes()
	_ = c.Handle(conn, &api.SubscribeBluetoothConnectionsFreeRequest{})
	connect(t, c, conn, testAddr)
	for len(conn.got) > 0 {
		<-conn.got
	}
	f.lost("C0:FF:EE:12:34:56")
	if _, ok := (<-conn.got).(*api.BluetoothDeviceConnectionResponse); !ok {
		t.Fatal("free slots announced before the disconnect")
	}
}

func TestAskingTwiceDoesNotFailTheConnection(t *testing.T) {
	c, _, conn := newFakes()
	connect(t, c, conn, testAddr)
	if r := connect(t, c, conn, testAddr); !r.Connected || r.Mtu != 247 {
		t.Fatalf("second connect %v", r)
	}
	if free := c.free(); free.Free != slots-1 {
		t.Fatalf("free %d after asking twice", free.Free)
	}
}

func TestConnectRetriesAFailedAttempt(t *testing.T) {
	c, f, conn := newFakes()
	f.failFirst = connectTries - 1
	if r := connect(t, c, conn, testAddr); !r.Connected {
		t.Fatalf("not connected after %d failures: %v", connectTries-1, r)
	}
}

func TestRefusedWhileConnectionsAreOff(t *testing.T) {
	c, f, conn := newFakes()
	c.allowed = func() bool { return false }
	if r := connect(t, c, conn, testAddr); r.Connected || r.Error != espNoResources {
		t.Fatalf("connect with connections off: %v", r)
	}
	_ = c.Handle(conn, &api.BluetoothGATTReadRequest{Address: testAddr, Handle: 3})
	if e := next[*api.BluetoothGATTErrorResponse](t, conn); e.Handle != 3 || e.Error == 0 {
		t.Fatalf("read with connections off: %v", e)
	}
	_ = c.Handle(conn, &api.SubscribeBluetoothConnectionsFreeRequest{})
	if free := next[*api.BluetoothConnectionsFreeResponse](t, conn); free.Free != 0 || free.Limit != 0 {
		t.Fatalf("free slots with connections off: %v", free)
	}
	if len(f.connected) != 0 {
		t.Fatalf("central asked anyway: %v", f.connected)
	}
}

func TestClosedSessionReleasesItsDevices(t *testing.T) {
	c, f, conn := newFakes()
	other := &fakeConn{got: make(chan proto.Message, 64)}
	connect(t, c, conn, testAddr)
	connect(t, c, other, testAddr+1)

	conn.dead = true
	c.ReleaseGone()
	deadline := time.Now().Add(2 * time.Second)
	for c.free().Free != slots-1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if free := c.free(); free.Free != slots-1 || len(free.Allocated) != 1 || free.Allocated[0] != testAddr+1 {
		t.Fatalf("after the session closed: %v", free)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.connected["C0:FF:EE:12:34:56"] || !f.connected["C0:FF:EE:12:34:57"] {
		t.Fatalf("central: %v", f.connected)
	}
}

func TestQueuedRequestsAnsweredWhenTheDeviceGoes(t *testing.T) {
	c, f, conn := newFakes()
	connect(t, c, conn, testAddr)
	f.readGate = make(chan struct{})
	_ = c.Handle(conn, &api.BluetoothGATTReadRequest{Address: testAddr, Handle: 1}) // holds the worker
	time.Sleep(20 * time.Millisecond)
	_ = c.Handle(conn, &api.BluetoothGATTReadRequest{Address: testAddr, Handle: 2}) // waits behind it

	f.lost("C0:FF:EE:12:34:56")
	if e := next[*api.BluetoothGATTErrorResponse](t, conn); e.Handle != 2 {
		t.Fatalf("queued read not answered: %v", e)
	}
	close(f.readGate)
}

func TestAddressIsMaskedTo48Bits(t *testing.T) {
	c, _, conn := newFakes()
	connect(t, c, conn, testAddr|1<<48)
	if r := connect(t, c, conn, testAddr); !r.Connected {
		t.Fatalf("same device by its 48 bits: %v", r)
	}
	if free := c.free(); free.Free != slots-1 || free.Allocated[0] != testAddr {
		t.Fatalf("one device became two peers: %v", free)
	}
}
