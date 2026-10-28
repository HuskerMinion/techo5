//go:build panel

package bluetooth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

const panelTestAddress = "02:00:00:00:00:01"
const panelTestRaw = "020000000001"
const panelTestUUID = "00001800-0000-1000-8000-00805f9b34fb"

// A fake Android socket exercises the transport; the shared manager's tests
// separately cover API ownership, free slots, queue draining and abandoned clients.
func panelFixture(t *testing.T, hook func(map[string]json.RawMessage) *panelEvent) (*panelCentral, chan panelEvent, chan string) {
	t.Helper()
	notifications := make(chan panelEvent, 4)
	lost := make(chan string, 4)
	c := newPanelCentral(func() (net.Conn, error) {
		client, server := net.Pipe()
		t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
		go func() {
			defer server.Close()
			encoder := json.NewEncoder(server)
			var mu sync.Mutex
			send := func(e panelEvent) { mu.Lock(); defer mu.Unlock(); _ = encoder.Encode(e) }
			done := make(chan struct{})
			defer close(done)
			go func() {
				for {
					select {
					case e := <-notifications:
						send(e)
					case <-done:
						return
					}
				}
			}()
			decoder := json.NewDecoder(server)
			for {
				var cmd map[string]json.RawMessage
				if decoder.Decode(&cmd) != nil {
					return
				}
				var op string
				_ = json.Unmarshal(cmd["op"], &op)
				var handle uint16
				_ = json.Unmarshal(cmd["handle"], &handle)
				var e *panelEvent
				if hook != nil {
					e = hook(cmd)
				}
				if e == nil {
					event := panelEvent{Addr: panelTestRaw, Type: op, Handle: handle}
					switch op {
					case "known":
						event.Known = true
					case "connect":
						event.Type = "connection"
						event.Connected = true
						event.MTU = 23
					case "services":
						event.Services = []panelService{{Handle: 1, UUID: panelTestUUID, Characteristics: []panelCharacteristic{{Handle: 3, UUID: panelTestUUID, Properties: 0x12, Descriptors: []panelDescriptor{{Handle: 4, UUID: panelTestUUID}}}}}}
					case "read_descriptor":
						event.Type = "read"
						event.Data = []byte{1, 0}
					case "read":
						event.Data = []byte{42}
					}
					e = &event
				}
				if e.Type != "drop" {
					send(*e)
				}
			}
		}()
		return client, nil
	}, nil, func(a string) { lost <- a })
	t.Cleanup(func() {
		c.mu.Lock()
		var peers []*panelPeer
		for _, p := range c.peers {
			peers = append(peers, p)
		}
		c.mu.Unlock()
		for _, p := range peers {
			p.finish(io.EOF, false)
		}
	})
	return c, notifications, lost
}
func connectPanel(t *testing.T, c *panelCentral) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Known(ctx, panelTestAddress); err != nil {
		t.Fatal(err)
	}
	if mtu, err := c.Connect(ctx, panelTestAddress, true); err != nil || mtu != 23 {
		t.Fatalf("connect: %d %v", mtu, err)
	}
}
func TestPanelTransportOperations(t *testing.T) {
	c, events, lost := panelFixture(t, nil)
	connectPanel(t, c)
	services, err := c.Services(panelTestAddress)
	if err != nil || len(services) != 1 || len(services[0].Characteristics[0].Flags) != 2 {
		t.Fatalf("services: %v %v", services, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	data, err := c.Read(ctx, panelTestAddress, 4)
	if err != nil || len(data) != 2 {
		t.Fatalf("descriptor read: %v %v", data, err)
	}
	if err := c.Write(ctx, panelTestAddress, 3, []byte{1}, false); err != nil {
		t.Fatal(err)
	}
	if err := c.Notify(ctx, panelTestAddress, 3, true); err != nil {
		t.Fatal(err)
	}
	got := make(chan []byte, 1)
	c.value = func(a string, h uint16, b []byte) {
		if a == panelTestAddress && h == 3 {
			got <- b
		}
	}
	events <- panelEvent{Type: "notify_data", Addr: panelTestRaw, Handle: 3, Data: []byte{7}}
	select {
	case b := <-got:
		if len(b) != 1 || b[0] != 7 {
			t.Fatal(b)
		}
	case <-ctx.Done():
		t.Fatal("no notification")
	}
	if err := c.Disconnect(ctx, panelTestAddress); err != nil {
		t.Fatal(err)
	}
	select {
	case <-lost:
		t.Fatal("requested disconnect reported as loss")
	default:
	}
	if _, err := c.Services(panelTestAddress); err == nil {
		t.Fatal("services remained after disconnect")
	}
}
func TestPanelNativeError(t *testing.T) {
	c, _, _ := panelFixture(t, func(cmd map[string]json.RawMessage) *panelEvent {
		if string(cmd["op"]) == `"read"` {
			return &panelEvent{Type: "error", Addr: panelTestRaw, Handle: 3, Error: 2}
		}
		return nil
	})
	connectPanel(t, c)
	_, err := c.Read(context.Background(), panelTestAddress, 3)
	if err == nil || gattError(readError{err}) != attReadNotPermitted {
		t.Fatalf("status: %v", err)
	}
}

func TestPanelDropsZeroHandleNotifications(t *testing.T) {
	c, events, _ := panelFixture(t, nil)
	got := make(chan panelEvent, 2)
	c.value = func(a string, h uint16, data []byte) {
		got <- panelEvent{Addr: a, Handle: h, Data: data}
	}
	connectPanel(t, c)
	events <- panelEvent{Type: "notify_data", Addr: panelTestRaw, Handle: 0, Data: []byte{9}}
	events <- panelEvent{Type: "notify_data", Addr: panelTestRaw, Handle: 3, Data: []byte{7}}
	select {
	case e := <-got:
		if e.Addr != panelTestAddress || e.Handle != 3 || len(e.Data) != 1 || e.Data[0] != 7 {
			t.Fatalf("unexpected notification: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("valid notification did not arrive after dropping handle zero")
	}
}
func TestPanelTimeoutClosesSession(t *testing.T) {
	c, _, lost := panelFixture(t, func(cmd map[string]json.RawMessage) *panelEvent {
		if string(cmd["op"]) == `"read"` {
			return &panelEvent{Type: "drop"}
		}
		return nil
	})
	connectPanel(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := c.Read(ctx, panelTestAddress, 3)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
	select {
	case <-lost:
	case <-time.After(time.Second):
		t.Fatal("slot owner not notified")
	}
	// A new session cannot consume a late callback from the cancelled socket.
	connectPanel(t, c)
}
func TestPanelUnexpectedDisconnect(t *testing.T) {
	c, events, lost := panelFixture(t, nil)
	connectPanel(t, c)
	events <- panelEvent{Type: "connection", Addr: panelTestRaw}
	select {
	case <-lost:
	case <-time.After(time.Second):
		t.Fatal("disconnect not reported")
	}
	if _, err := c.Services(panelTestAddress); err == nil {
		t.Fatal("lost peer remains")
	}
}
func TestPanelRejectsInvalidHandles(t *testing.T) {
	for _, h := range []int{0, 65536, 1} {
		_, _, err := panelServices([]panelService{{Handle: 1, UUID: panelTestUUID, Characteristics: []panelCharacteristic{{Handle: h, UUID: panelTestUUID}}}})
		if err == nil {
			t.Fatalf("accepted handle %d", h)
		}
	}
}
