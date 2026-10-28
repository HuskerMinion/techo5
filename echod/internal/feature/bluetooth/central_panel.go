//go:build panel

package bluetooth

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/bluez"
	"github.com/HuskerMinion/techo5/echod/internal/lib/panelsock"
	"github.com/google/uuid"
)

// The shared connections manager owns API clients, slots, queues and retries. This
// adapter supplies only Android's GATT transport, checked in both directions by UID.
var openCentral = func(value func(string, uint16, []byte), lost func(string)) (central, error) {
	return newPanelCentral(func() (net.Conn, error) { return panelsock.Dial("@techo5.gatt") }, value, lost), nil
}

type panelCentral struct {
	mu    sync.Mutex
	peers map[string]*panelPeer
	dial  func() (net.Conn, error)
	value func(string, uint16, []byte)
	lost  func(string)
}

type panelPeer struct {
	owner       *panelCentral
	address     string
	conn        net.Conn
	op          sync.Mutex
	mu          sync.Mutex
	pending     chan panelReply
	connected   bool
	closing     bool
	services    []bluez.Service
	descriptors map[uint16]bool
	done        chan struct{}
	once        sync.Once
}

type panelEvent struct {
	Type      string         `json:"type"`
	Addr      string         `json:"addr"`
	Handle    uint16         `json:"handle"`
	Connected bool           `json:"connected"`
	Known     bool           `json:"known"`
	MTU       int            `json:"mtu"`
	Error     int32          `json:"error"`
	Data      []byte         `json:"data"`
	Services  []panelService `json:"services"`
}

type panelService struct {
	UUID            string                `json:"uuid"`
	Handle          int                   `json:"handle"`
	Characteristics []panelCharacteristic `json:"characteristics"`
}
type panelCharacteristic struct {
	UUID        string            `json:"uuid"`
	Handle      int               `json:"handle"`
	Properties  uint32            `json:"properties"`
	Descriptors []panelDescriptor `json:"descriptors"`
}
type panelDescriptor struct {
	UUID   string `json:"uuid"`
	Handle int    `json:"handle"`
}
type panelReply struct {
	event panelEvent
	err   error
}
type panelStatus int32

func (s panelStatus) Error() string { return fmt.Sprintf("Android GATT status 0x%02x", int32(s)) }
func (s panelStatus) GATTStatus() int32 {
	if s > 0 && s <= 255 {
		return int32(s)
	}
	return espGattError
}

func newPanelCentral(dial func() (net.Conn, error), value func(string, uint16, []byte), lost func(string)) *panelCentral {
	return &panelCentral{peers: make(map[string]*panelPeer), dial: dial, value: value, lost: lost}
}
func (c *panelCentral) peer(address string, create bool) (*panelPeer, error) {
	a, ok := addressValue(address)
	if !ok {
		return nil, errors.New("invalid Bluetooth address")
	}
	address = addressString(a)
	c.mu.Lock()
	defer c.mu.Unlock()
	if p := c.peers[address]; p != nil {
		return p, nil
	}
	if !create {
		return nil, errors.New("Bluetooth device is not connected")
	}
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	p := &panelPeer{owner: c, address: address, conn: conn, done: make(chan struct{})}
	c.peers[address] = p
	go p.listen()
	return p, nil
}

func (p *panelPeer) finish(err error, reportLoss bool) {
	p.once.Do(func() {
		p.owner.mu.Lock()
		if p.owner.peers[p.address] == p {
			delete(p.owner.peers, p.address)
		}
		p.owner.mu.Unlock()
		p.mu.Lock()
		lost := p.connected && !p.closing && reportLoss
		p.closing = true
		if p.pending != nil {
			select {
			case p.pending <- panelReply{err: err}:
			default:
			}
		}
		close(p.done)
		p.mu.Unlock()
		_ = p.conn.Close()
		if lost && p.owner.lost != nil {
			p.owner.lost(p.address)
		}
	})
}
func (p *panelPeer) listen() {
	scanner := bufio.NewScanner(p.conn)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var e panelEvent
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			p.finish(err, true)
			return
		}
		// A reply for another address cannot satisfy this peer's operation.
		if !strings.EqualFold(e.Addr, strings.ReplaceAll(p.address, ":", "")) {
			p.finish(errors.New("GATT reply address mismatch"), true)
			return
		}
		if e.Type == "notify_data" {
			p.mu.Lock()
			active := p.connected && !p.closing
			p.mu.Unlock()
			if active && e.Handle != 0 && p.owner.value != nil {
				p.owner.value(p.address, e.Handle, e.Data)
			}
			continue
		}
		p.mu.Lock()
		if e.Type == "connection" && e.Connected {
			p.connected = true
		}
		pending := p.pending
		unexpectedLoss := e.Type == "connection" && !e.Connected && p.connected && !p.closing
		if pending != nil {
			select {
			case pending <- panelReply{event: e}:
			default:
			}
		}
		p.mu.Unlock()
		if unexpectedLoss {
			p.finish(io.EOF, true)
			return
		}
	}
	err := scanner.Err()
	if err == nil {
		err = io.EOF
	}
	p.finish(err, true)
}

func (p *panelPeer) request(ctx context.Context, command map[string]any, want string, handle uint16) (panelEvent, error) {
	p.op.Lock()
	defer p.op.Unlock()
	if err := ctx.Err(); err != nil {
		return panelEvent{}, err
	}
	ch := make(chan panelReply, 1)
	p.mu.Lock()
	if p.closing {
		p.mu.Unlock()
		return panelEvent{}, io.EOF
	}
	p.pending = ch
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.pending = nil; p.mu.Unlock() }()
	command["addr"] = strings.ReplaceAll(p.address, ":", "")
	deadline := time.Now().Add(opTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = p.conn.SetWriteDeadline(deadline)
	if err := json.NewEncoder(p.conn).Encode(command); err != nil {
		p.finish(err, true)
		return panelEvent{}, err
	}
	var r panelReply
	select {
	case r = <-ch:
	case <-ctx.Done():
		p.finish(ctx.Err(), true)
		return panelEvent{}, ctx.Err()
	case <-p.done:
		return panelEvent{}, io.EOF
	}
	if r.err != nil {
		return panelEvent{}, r.err
	}
	e := r.event
	if e.Error != 0 {
		return e, panelStatus(e.Error)
	}
	if e.Type != want || e.Handle != handle {
		err := errors.New("unexpected GATT reply")
		p.finish(err, true)
		return e, err
	}
	return e, nil
}

func (c *panelCentral) Known(ctx context.Context, address string) error {
	p, err := c.peer(address, true)
	if err != nil {
		return err
	}
	for {
		e, err := p.request(ctx, map[string]any{"op": "known"}, "known", 0)
		if err != nil {
			return err
		}
		if e.Known {
			return nil
		}
		select {
		case <-ctx.Done():
			p.finish(ctx.Err(), false)
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
func (c *panelCentral) Connect(ctx context.Context, address string, random bool) (int, error) {
	p, err := c.peer(address, true)
	if err != nil {
		return 0, err
	}
	e, err := p.request(ctx, map[string]any{"op": "connect", "random": random}, "connection", 0)
	if err != nil || !e.Connected {
		p.finish(io.EOF, false)
		if err == nil {
			err = io.EOF
		}
		return 0, err
	}
	// Populate real descriptor handles even when HA uses its cached service tree.
	tree, err := p.request(ctx, map[string]any{"op": "services"}, "services", 0)
	if err != nil {
		p.finish(err, false)
		return 0, err
	}
	services, descriptors, err := panelServices(tree.Services)
	if err != nil {
		p.finish(err, false)
		return 0, err
	}
	p.mu.Lock()
	p.services = services
	p.descriptors = descriptors
	p.mu.Unlock()
	if e.MTU < 23 || e.MTU > 517 {
		p.finish(errors.New("invalid GATT MTU"), false)
		return 0, errors.New("invalid GATT MTU")
	}
	return e.MTU, nil
}
func (c *panelCentral) Disconnect(ctx context.Context, address string) error {
	p, err := c.peer(address, false)
	if err != nil {
		return nil
	}
	p.mu.Lock()
	p.closing = true
	p.mu.Unlock()
	// Closing the local socket makes Android disconnect and close BluetoothGatt in
	// Session.finally, including when an Android disconnect callback never arrives.
	p.finish(io.EOF, false)
	return nil
}
func (c *panelCentral) Services(address string) ([]bluez.Service, error) {
	p, err := c.peer(address, false)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.connected || p.closing {
		return nil, io.EOF
	}
	return p.services, nil
}
func (c *panelCentral) Read(ctx context.Context, address string, handle uint16) ([]byte, error) {
	p, err := c.peer(address, false)
	if err != nil {
		return nil, err
	}
	op := "read"
	p.mu.Lock()
	if p.descriptors[handle] {
		op = "read_descriptor"
	}
	p.mu.Unlock()
	e, err := p.request(ctx, map[string]any{"op": op, "handle": handle}, "read", handle)
	return e.Data, err
}
func (c *panelCentral) Write(ctx context.Context, address string, handle uint16, data []byte, response bool) error {
	p, err := c.peer(address, false)
	if err != nil {
		return err
	}
	_, err = p.request(ctx, map[string]any{"op": "write", "handle": handle, "data": data, "response": response}, "write", handle)
	return err
}
func (c *panelCentral) Notify(ctx context.Context, address string, handle uint16, on bool) error {
	p, err := c.peer(address, false)
	if err != nil {
		return err
	}
	_, err = p.request(ctx, map[string]any{"op": "notify", "handle": handle, "enable": on}, "notify", handle)
	return err
}

func panelServices(in []panelService) ([]bluez.Service, map[uint16]bool, error) {
	seen := map[int]bool{}
	descriptors := map[uint16]bool{}
	valid := func(h int, u string) bool {
		_, err := uuid.Parse(u)
		if err != nil || h <= 0 || h > 65535 || seen[h] {
			return false
		}
		seen[h] = true
		return true
	}
	flags := []string{"broadcast", "read", "write-without-response", "write", "notify", "indicate", "authenticated-signed-writes", "extended-properties"}
	var out []bluez.Service
	for _, s := range in {
		if !valid(s.Handle, s.UUID) {
			return nil, nil, errors.New("invalid GATT service handle or UUID")
		}
		service := bluez.Service{Handle: uint16(s.Handle), UUID: s.UUID}
		for _, ch := range s.Characteristics {
			if !valid(ch.Handle, ch.UUID) {
				return nil, nil, errors.New("invalid GATT characteristic handle or UUID")
			}
			characteristic := bluez.Characteristic{Handle: uint16(ch.Handle), UUID: ch.UUID}
			for bit, flag := range flags {
				if ch.Properties&(1<<bit) != 0 {
					characteristic.Flags = append(characteristic.Flags, flag)
				}
			}
			for _, d := range ch.Descriptors {
				if !valid(d.Handle, d.UUID) {
					return nil, nil, errors.New("invalid GATT descriptor handle or UUID")
				}
				characteristic.Descriptors = append(characteristic.Descriptors, bluez.Descriptor{Handle: uint16(d.Handle), UUID: d.UUID})
				descriptors[uint16(d.Handle)] = true
			}
			service.Characteristics = append(service.Characteristics, characteristic)
		}
		out = append(out, service)
	}
	return out, descriptors, nil
}
