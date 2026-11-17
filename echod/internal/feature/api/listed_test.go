package api

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"
)

// haClient speaks the plaintext API as Home Assistant does, enough to connect, list and subscribe.
type haClient struct {
	t  *testing.T
	nc net.Conn
	r  *bufio.Reader
}

func dialHA(t *testing.T, addr string) *haClient {
	t.Helper()
	nc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nc.Close() })
	return &haClient{t: t, nc: nc, r: bufio.NewReader(nc)}
}

func (h *haClient) send(msgs ...proto.Message) {
	h.t.Helper()
	var buf []byte
	for _, m := range msgs {
		info, ok := esphome.Lookup(m)
		if !ok {
			h.t.Fatalf("%T has no id", m)
		}
		data, err := proto.Marshal(m)
		if err != nil {
			h.t.Fatal(err)
		}
		buf = append(buf, 0)
		buf = binary.AppendUvarint(buf, uint64(len(data)))
		buf = binary.AppendUvarint(buf, uint64(info.ID))
		buf = append(buf, data...)
	}
	if _, err := h.nc.Write(buf); err != nil {
		h.t.Fatal(err)
	}
}

// read is the next message, or the error that ended the connection.
func (h *haClient) read(within time.Duration) (proto.Message, error) {
	_ = h.nc.SetReadDeadline(time.Now().Add(within))
	if _, err := binary.ReadUvarint(h.r); err != nil {
		return nil, err
	}
	n, err := binary.ReadUvarint(h.r)
	if err != nil {
		return nil, err
	}
	id, err := binary.ReadUvarint(h.r)
	if err != nil {
		return nil, err
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(h.r, data); err != nil {
		return nil, err
	}
	m, ok := esphome.NewMessage(uint32(id))
	if !ok {
		return nil, errors.New("unknown message")
	}
	return m, proto.Unmarshal(data, m)
}

// until reads up to the first message of want's type.
func until[T proto.Message](h *haClient) T {
	h.t.Helper()
	for {
		m, err := h.read(5 * time.Second)
		if err != nil {
			h.t.Fatalf("waiting for %T: %v", *new(T), err)
		}
		if got, ok := m.(T); ok {
			return got
		}
	}
}

// connect is Home Assistant's connect up to the entity list, which is where one that stopped stopped.
func (h *haClient) connect() {
	h.send(&api.HelloRequest{ClientInfo: "Home Assistant", ApiVersionMajor: 1, ApiVersionMinor: 12})
	until[*api.HelloResponse](h)
	h.send(&api.DeviceInfoRequest{}, &api.ListEntitiesRequest{})
	until[*api.ListEntitiesDoneResponse](h)
}

// serveListed serves one switch, on, behind the watch, as the device does.
func serveListed(t *testing.T) (addr string, sw *esphome.Switch) {
	t.Helper()
	saved := subscribeWithin
	subscribeWithin = 300 * time.Millisecond
	t.Cleanup(func() { subscribeWithin = saved })

	sw = &esphome.Switch{Base: esphome.Base{ObjectID: "mic_mute", Name: "Mic mute"}}
	sw.Set(true)
	ents := esphome.NewEntities()
	if err := ents.Add(sw); err != nil {
		t.Fatal(err)
	}
	srv := &esphome.Server{Info: esphome.Info{Name: "test"}, Handler: esphome.Chain(newUnsubscribed(), ents)}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = srv.Serve(ctx, ln); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return ln.Addr().String(), sw
}

// A Home Assistant that listed the entities and stopped before asking for their states had the device
// connected and every entity unavailable, until the daemon restarted. The connection is dropped
// instead, and the connect after it gets the states.
func TestAConnectThatNeverAsksForStatesIsDropped(t *testing.T) {
	addr, _ := serveListed(t)

	stopped := dialHA(t, addr)
	stopped.connect()
	// Pings answered all along, as Home Assistant's keepalive saw them.
	stopped.send(&api.PingRequest{})
	until[*api.PingResponse](stopped)

	for {
		_, err := stopped.read(5 * time.Second)
		if err == nil {
			continue
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			t.Fatal("a connection that never asked for the states is still open")
		}
		break
	}

	again := dialHA(t, addr)
	again.connect()
	again.send(&api.SubscribeStatesRequest{})
	if st := until[*api.SwitchStateResponse](again); !st.GetState() {
		t.Errorf("the connect after the drop got the switch %v, want on", st.GetState())
	}
}

// A connection that asked for the states is Home Assistant connected, and is left alone.
func TestAConnectThatAsksForStatesIsKept(t *testing.T) {
	addr, sw := serveListed(t)

	h := dialHA(t, addr)
	h.connect()
	h.send(&api.SubscribeStatesRequest{})
	until[*api.SwitchStateResponse](h)

	time.Sleep(3 * subscribeWithin)
	sw.Set(false)
	if st := until[*api.SwitchStateResponse](h); st.GetState() {
		t.Error("a change after the window did not arrive")
	}
	h.send(&api.PingRequest{})
	until[*api.PingResponse](h)
}

// A client that never lists the entities (a log viewer) is not Home Assistant stuck partway, and stays.
func TestAClientThatNeverListsIsKept(t *testing.T) {
	addr, _ := serveListed(t)

	h := dialHA(t, addr)
	h.send(&api.HelloRequest{ClientInfo: "logs"})
	until[*api.HelloResponse](h)
	h.send(&api.SubscribeLogsRequest{Level: api.LogLevel_LOG_LEVEL_INFO})

	time.Sleep(3 * subscribeWithin)
	h.send(&api.PingRequest{})
	until[*api.PingResponse](h)
}
