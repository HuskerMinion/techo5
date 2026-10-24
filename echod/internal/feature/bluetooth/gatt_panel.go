//go:build panel

package bluetooth

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"
	"google.golang.org/protobuf/proto"

	"github.com/HuskerMinion/techo5/echod/internal/lib/panelsock"
)

// Android owns the GATT client. One same-UID abstract socket carries commands and callbacks;
// this proxy deliberately offers one connection slot until its Android lifecycle is established.
const gattSocket = "@techo5.gatt"
const addressMask = uint64(1<<48 - 1)

type gattBridge struct {
	mu          sync.Mutex
	writeMu     sync.Mutex
	sock        net.Conn
	client      *esphome.Conn
	subscribers map[*esphome.Conn]struct{}
	address     uint64
	connected   bool
}

var panelGatt gattBridge

type gattCommand struct {
	Op       string `json:"op"`
	Addr     string `json:"addr,omitempty"`
	Handle   uint32 `json:"handle,omitempty"`
	Data     []byte `json:"data,omitempty"`
	Response bool   `json:"response,omitempty"`
	Enable   bool   `json:"enable,omitempty"`
}

type gattDescriptor struct {
	UUID   string `json:"uuid"`
	Handle uint32 `json:"handle"`
}

type gattCharacteristic struct {
	UUID        string           `json:"uuid"`
	Handle      uint32           `json:"handle"`
	Properties  uint32           `json:"properties"`
	Descriptors []gattDescriptor `json:"descriptors"`
}

type panelGattService struct {
	UUID            string               `json:"uuid"`
	Handle          uint32               `json:"handle"`
	Characteristics []gattCharacteristic `json:"characteristics"`
}

type gattEvent struct {
	Type      string             `json:"type"`
	Addr      string             `json:"addr"`
	Handle    uint32             `json:"handle"`
	Connected bool               `json:"connected"`
	MTU       uint32             `json:"mtu"`
	Error     int32              `json:"error"`
	Data      []byte             `json:"data"`
	Services  []panelGattService `json:"services"`
}

func (b *Proxy) gattFeatures() esphome.BluetoothFeature {
	if err := panelGatt.ensure(); err != nil {
		slog.Warn("panel GATT bridge unavailable", "err", err)
		return 0
	}
	slog.Info("panel GATT bridge available")
	// V3 connect requests require REMOTE_CACHING. The Android bridge rediscovers services on
	// every connection and assigns deterministic handles, including WITH_CACHE requests.
	return esphome.BluetoothActiveConnections | esphome.BluetoothRemoteCaching
}

func (g *gattBridge) ensure() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sock != nil {
		return nil
	}
	c, err := panelsock.Dial(gattSocket)
	if err != nil {
		return err
	}
	g.sock = c
	go g.read(c)
	return nil
}

func (g *gattBridge) command(cmd gattCommand) error {
	if err := g.ensure(); err != nil {
		return err
	}
	data, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	g.writeMu.Lock()
	defer g.writeMu.Unlock()
	g.mu.Lock()
	c := g.sock
	g.mu.Unlock()
	if c == nil {
		return fmt.Errorf("panel GATT socket closed")
	}
	_, err = io.Copy(c, bytes.NewReader(append(data, '\n')))
	return err
}

func (g *gattBridge) allowed(c *esphome.Conn, address uint64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.connected && g.address == address && g.client == c
}

func (g *gattBridge) read(c net.Conn) {
	defer func() {
		g.mu.Lock()
		same := g.sock == c
		owner, addr := g.client, g.address
		if same {
			g.sock, g.client, g.address, g.connected = nil, nil, 0, false
		}
		g.mu.Unlock()
		_ = c.Close()
		if same && owner != nil && addr != 0 {
			_ = owner.Send(&api.BluetoothDeviceConnectionResponse{Address: addr, Error: 1})
		}
		if same {
			g.freeAll()
		}
	}()
	scan := bufio.NewScanner(c)
	scan.Buffer(make([]byte, 16*1024), 1024*1024)
	for scan.Scan() {
		var event gattEvent
		if err := json.Unmarshal(scan.Bytes(), &event); err != nil {
			slog.Warn("panel GATT event malformed", "err", err)
			continue
		}
		g.event(event)
	}
	if err := scan.Err(); err != nil {
		slog.Warn("panel GATT bridge ended", "err", err)
	}
}

func (g *gattBridge) event(e gattEvent) {
	addr, err := strconv.ParseUint(e.Addr, 16, 48)
	if err != nil {
		return
	}
	g.mu.Lock()
	c := g.client
	// Android callbacks can arrive after a disconnect or an owner change. Never
	// attribute an old device's reply to the client now using the single slot.
	if c == nil || g.address != addr {
		g.mu.Unlock()
		return
	}
	if e.Type == "connection" {
		g.connected = e.Connected
		if !e.Connected {
			g.address = 0
			g.client = nil
		}
	}
	g.mu.Unlock()
	switch e.Type {
	case "connection":
		_ = c.Send(&api.BluetoothDeviceConnectionResponse{Address: addr, Connected: e.Connected,
			Mtu: e.MTU, Error: e.Error})
		g.freeAll()
	case "services":
		for _, service := range e.Services {
			converted := convertService(service)
			if converted == nil {
				continue
			}
			if err := c.Send(&api.BluetoothGATTGetServicesResponse{Address: addr,
				Services: []*api.BluetoothGATTService{converted}}); err != nil {
				return
			}
		}
		_ = c.Send(&api.BluetoothGATTGetServicesDoneResponse{Address: addr})
	case "read":
		_ = c.Send(&api.BluetoothGATTReadResponse{Address: addr, Handle: e.Handle, Data: e.Data})
	case "write":
		_ = c.Send(&api.BluetoothGATTWriteResponse{Address: addr, Handle: e.Handle})
	case "notify":
		_ = c.Send(&api.BluetoothGATTNotifyResponse{Address: addr, Handle: e.Handle})
	case "notify_data":
		_ = c.Send(&api.BluetoothGATTNotifyDataResponse{Address: addr, Handle: e.Handle, Data: e.Data})
	case "error":
		_ = c.Send(&api.BluetoothGATTErrorResponse{Address: addr, Handle: e.Handle, Error: e.Error})
	}
}

func parseUUID(value string) []uint64 {
	compact := strings.ReplaceAll(value, "-", "")
	raw, err := hex.DecodeString(compact)
	if err != nil || len(raw) != 16 {
		return nil
	}
	return []uint64{binary.BigEndian.Uint64(raw[:8]), binary.BigEndian.Uint64(raw[8:])}
}

func convertService(s panelGattService) *api.BluetoothGATTService {
	uuid := parseUUID(s.UUID)
	if uuid == nil {
		return nil
	}
	out := &api.BluetoothGATTService{Uuid: uuid, Handle: s.Handle}
	for _, ch := range s.Characteristics {
		cuuid := parseUUID(ch.UUID)
		if cuuid == nil {
			continue
		}
		c := &api.BluetoothGATTCharacteristic{Uuid: cuuid, Handle: ch.Handle, Properties: ch.Properties}
		for _, desc := range ch.Descriptors {
			duuid := parseUUID(desc.UUID)
			if duuid == nil {
				continue
			}
			c.Descriptors = append(c.Descriptors, &api.BluetoothGATTDescriptor{Uuid: duuid, Handle: desc.Handle})
		}
		out.Characteristics = append(out.Characteristics, c)
	}
	return out
}

func (g *gattBridge) free(c *esphome.Conn) {
	_ = c.Send(g.freeResponse())
}

func (g *gattBridge) freeAll() {
	g.mu.Lock()
	clients := make([]*esphome.Conn, 0, len(g.subscribers))
	for c := range g.subscribers {
		clients = append(clients, c)
	}
	g.mu.Unlock()
	for _, c := range clients {
		if err := c.Send(g.freeResponse()); err != nil {
			g.mu.Lock()
			delete(g.subscribers, c)
			g.mu.Unlock()
		}
	}
}

func (g *gattBridge) freeResponse() *api.BluetoothConnectionsFreeResponse {
	g.mu.Lock()
	addr := g.address
	g.mu.Unlock()
	response := &api.BluetoothConnectionsFreeResponse{Limit: 1, Free: 1}
	if addr != 0 {
		response.Free, response.Allocated = 0, []uint64{addr}
	}
	return response
}

// The ESPHome API does not notify handlers when a client socket closes. A periodic slot update
// detects a vanished owner, while scanner-unsubscribe wakes the same check sooner when possible.
func (g *gattBridge) watch(c *esphome.Conn) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		g.mu.Lock()
		current := g.client == c
		g.mu.Unlock()
		if !current {
			return
		}
		if err := c.Send(g.freeResponse()); err != nil {
			g.ownerGone(c)
			return
		}
	}
}

func (g *gattBridge) ownerGone(c *esphome.Conn) {
	g.mu.Lock()
	if g.client != c {
		g.mu.Unlock()
		return
	}
	addr := g.address
	g.client, g.address, g.connected = nil, 0, false
	delete(g.subscribers, c)
	g.mu.Unlock()
	if addr != 0 {
		_ = g.command(gattCommand{Op: "disconnect"})
	}
	g.freeAll()
}

func (b *Proxy) gattProbeOwner() {
	g := &panelGatt
	g.mu.Lock()
	c := g.client
	g.mu.Unlock()
	if c != nil {
		if err := c.Send(g.freeResponse()); err != nil {
			g.ownerGone(c)
		}
	}
}

func (b *Proxy) gattDisable() {
	g := &panelGatt
	g.mu.Lock()
	c, addr := g.client, g.address
	g.client, g.address, g.connected = nil, 0, false
	g.mu.Unlock()
	if addr != 0 {
		_ = g.command(gattCommand{Op: "disconnect"})
	}
	if c != nil && addr != 0 {
		_ = c.Send(&api.BluetoothDeviceConnectionResponse{Address: addr, Error: 1})
	}
	g.freeAll()
}

func (g *gattBridge) reject(c *esphome.Conn, address uint64, handle uint32, err error) {
	slog.Warn("panel GATT command failed", "err", err)
	_ = c.Send(&api.BluetoothGATTErrorResponse{Address: address, Handle: handle, Error: 1})
}

func (b *Proxy) gattHandle(_ context.Context, c *esphome.Conn, msg proto.Message) (bool, error) {
	g := &panelGatt
	if !b.ConnectionsEnabled() {
		switch m := msg.(type) {
		case *api.SubscribeBluetoothConnectionsFreeRequest:
			return true, c.Send(&api.BluetoothConnectionsFreeResponse{})
		case *api.BluetoothDeviceRequest:
			return true, c.Send(&api.BluetoothDeviceConnectionResponse{Address: m.GetAddress() & addressMask, Error: 1})
		case *api.BluetoothGATTGetServicesRequest:
			return true, c.Send(&api.BluetoothGATTErrorResponse{Address: m.GetAddress() & addressMask, Error: 1})
		case *api.BluetoothGATTReadRequest:
			return true, c.Send(&api.BluetoothGATTErrorResponse{Address: m.GetAddress() & addressMask, Handle: m.GetHandle(), Error: 1})
		case *api.BluetoothGATTReadDescriptorRequest:
			return true, c.Send(&api.BluetoothGATTErrorResponse{Address: m.GetAddress() & addressMask, Handle: m.GetHandle(), Error: 1})
		case *api.BluetoothGATTWriteRequest:
			return true, c.Send(&api.BluetoothGATTErrorResponse{Address: m.GetAddress() & addressMask, Handle: m.GetHandle(), Error: 1})
		case *api.BluetoothGATTWriteDescriptorRequest:
			return true, c.Send(&api.BluetoothGATTErrorResponse{Address: m.GetAddress() & addressMask, Handle: m.GetHandle(), Error: 1})
		case *api.BluetoothGATTNotifyRequest:
			return true, c.Send(&api.BluetoothGATTErrorResponse{Address: m.GetAddress() & addressMask, Handle: m.GetHandle(), Error: 1})
		}
	}
	switch m := msg.(type) {
	case *api.SubscribeBluetoothConnectionsFreeRequest:
		if err := g.ensure(); err != nil {
			return true, c.Send(&api.BluetoothConnectionsFreeResponse{})
		}
		g.mu.Lock()
		if g.subscribers == nil {
			g.subscribers = make(map[*esphome.Conn]struct{})
		}
		g.subscribers[c] = struct{}{}
		g.mu.Unlock()
		g.free(c)
		return true, nil
	case *api.BluetoothDeviceRequest:
		addr := m.GetAddress() & addressMask
		cmd := gattCommand{Addr: fmt.Sprintf("%012X", addr)}
		switch m.GetRequestType() {
		case api.BluetoothDeviceRequestType_BLUETOOTH_DEVICE_REQUEST_TYPE_CONNECT_V3_WITH_CACHE,
			api.BluetoothDeviceRequestType_BLUETOOTH_DEVICE_REQUEST_TYPE_CONNECT_V3_WITHOUT_CACHE:
			g.mu.Lock()
			busy := g.address != 0 && (g.address != addr || g.client != c)
			newOwner := g.client != c
			if !busy {
				g.client = c
				g.address = addr
				if g.subscribers == nil {
					g.subscribers = make(map[*esphome.Conn]struct{})
				}
				g.subscribers[c] = struct{}{}
			}
			g.mu.Unlock()
			if busy {
				return true, c.Send(&api.BluetoothDeviceConnectionResponse{Address: addr, Error: 0x84})
			}
			if newOwner {
				go g.watch(c)
			}
			cmd.Op = "connect"
			g.freeAll()
		case api.BluetoothDeviceRequestType_BLUETOOTH_DEVICE_REQUEST_TYPE_DISCONNECT:
			g.mu.Lock()
			current, owner := g.address, g.client
			g.mu.Unlock()
			if current != addr {
				return true, c.Send(&api.BluetoothDeviceConnectionResponse{Address: addr})
			}
			if owner != c {
				return true, c.Send(&api.BluetoothDeviceConnectionResponse{Address: addr, Error: 0x84})
			}
			cmd.Op = "disconnect"
		default:
			return true, c.Send(&api.BluetoothDeviceConnectionResponse{Address: addr, Error: 1})
		}
		if err := g.command(cmd); err != nil {
			g.mu.Lock()
			g.client, g.address, g.connected = nil, 0, false
			g.mu.Unlock()
			g.freeAll()
			return true, c.Send(&api.BluetoothDeviceConnectionResponse{Address: addr, Error: 1})
		}
		return true, nil
	case *api.BluetoothGATTGetServicesRequest:
		if !g.allowed(c, m.GetAddress()&addressMask) {
			return true, c.Send(&api.BluetoothGATTErrorResponse{Address: m.GetAddress() & addressMask, Error: 1})
		}
		if err := g.command(gattCommand{Op: "services"}); err != nil {
			g.reject(c, m.GetAddress()&addressMask, 0, err)
		}
		return true, nil
	case *api.BluetoothGATTReadRequest:
		if !g.allowed(c, m.GetAddress()&addressMask) {
			return true, c.Send(&api.BluetoothGATTErrorResponse{Address: m.GetAddress() & addressMask, Handle: m.GetHandle(), Error: 1})
		}
		if err := g.command(gattCommand{Op: "read", Handle: m.GetHandle()}); err != nil {
			g.reject(c, m.GetAddress()&addressMask, m.GetHandle(), err)
		}
		return true, nil
	case *api.BluetoothGATTReadDescriptorRequest:
		if !g.allowed(c, m.GetAddress()&addressMask) {
			return true, c.Send(&api.BluetoothGATTErrorResponse{Address: m.GetAddress() & addressMask, Handle: m.GetHandle(), Error: 1})
		}
		if err := g.command(gattCommand{Op: "read", Handle: m.GetHandle()}); err != nil {
			g.reject(c, m.GetAddress()&addressMask, m.GetHandle(), err)
		}
		return true, nil
	case *api.BluetoothGATTWriteRequest:
		if !g.allowed(c, m.GetAddress()&addressMask) {
			return true, c.Send(&api.BluetoothGATTErrorResponse{Address: m.GetAddress() & addressMask, Handle: m.GetHandle(), Error: 1})
		}
		if err := g.command(gattCommand{Op: "write", Handle: m.GetHandle(), Data: m.GetData(), Response: m.GetResponse()}); err != nil {
			g.reject(c, m.GetAddress()&addressMask, m.GetHandle(), err)
		}
		return true, nil
	case *api.BluetoothGATTWriteDescriptorRequest:
		if !g.allowed(c, m.GetAddress()&addressMask) {
			return true, c.Send(&api.BluetoothGATTErrorResponse{Address: m.GetAddress() & addressMask, Handle: m.GetHandle(), Error: 1})
		}
		if err := g.command(gattCommand{Op: "write", Handle: m.GetHandle(), Data: m.GetData(), Response: true}); err != nil {
			g.reject(c, m.GetAddress()&addressMask, m.GetHandle(), err)
		}
		return true, nil
	case *api.BluetoothGATTNotifyRequest:
		if !g.allowed(c, m.GetAddress()&addressMask) {
			return true, c.Send(&api.BluetoothGATTErrorResponse{Address: m.GetAddress() & addressMask, Handle: m.GetHandle(), Error: 1})
		}
		if err := g.command(gattCommand{Op: "notify", Handle: m.GetHandle(), Enable: m.GetEnable()}); err != nil {
			g.reject(c, m.GetAddress()&addressMask, m.GetHandle(), err)
		}
		return true, nil
	}
	return false, nil
}
