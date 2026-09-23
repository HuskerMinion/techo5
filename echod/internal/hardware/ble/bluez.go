//go:build !dot

package ble

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/bluez"
)

// On the Show the controller belongs to bluetoothd, which the earbuds need, so the proxy cannot
// open the HCI node the way the Dot does. It scans through BlueZ instead: LE discovery on the
// adapter, and the advertisement rebuilt from the fields BlueZ parsed out of it, since Home
// Assistant wants the raw bytes. Advertising (the iBeacon) is not offered this way.
type bluezScanner struct {
	mu       sync.Mutex
	adapter  *bluez.Adapter
	cancel   func()
	stop     chan struct{}
	running  bool
	scanning bool
	reports  atomic.Uint64
}

var (
	bzOnce sync.Once
	bz     *bluezScanner
)

// Default is the scanner for this device: BlueZ's.
func Default() Scanner {
	bzOnce.Do(func() { bz = &bluezScanner{} })
	return bz
}

func (s *bluezScanner) Scanning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scanning
}

func (s *bluezScanner) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *bluezScanner) Reports() uint64 { return s.reports.Load() }

func (s *bluezScanner) Start(scan, active bool, advertisement []byte, found func(Advertisement)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return nil
	}
	if len(advertisement) != 0 {
		slog.Info("ble: advertising is not available through bluez, skipping the beacon")
	}
	if !scan {
		s.running = true
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	a, err := bluez.Open(ctx)
	cancel()
	if err != nil {
		return fmt.Errorf("ble: bluez: %w", err)
	}
	if err := a.DiscoverLE(true); err != nil {
		a.Close()
		return fmt.Errorf("ble: bluez discovery: %w", err)
	}
	s.adapter = a
	s.cancel = a.Advertised.Listen(func(d bluez.Device) {
		adv, ok := fromDevice(d)
		if !ok {
			return
		}
		s.reports.Add(1)
		found(adv)
	})
	s.stop = make(chan struct{})
	s.running, s.scanning = true, true
	// Pairing on the audio side starts and stops discovery too; keep ours going.
	go func(a *bluez.Adapter, stop chan struct{}) {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if err := a.DiscoverLE(true); err != nil {
					slog.Debug("ble: bluez discovery", "err", err)
				}
			}
		}
	}(a, s.stop)
	return nil
}

func (s *bluezScanner) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	if s.stop != nil {
		close(s.stop)
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.adapter != nil {
		_ = s.adapter.Discover(false)
		_ = s.adapter.Close()
	}
	s.adapter, s.cancel, s.stop = nil, nil, nil
	s.running, s.scanning = false, false
}

// fromDevice rebuilds an advertisement from what BlueZ knows about a device, in the byte order
// the HCI scanner delivers so both paths look the same to Home Assistant.
func fromDevice(d bluez.Device) (Advertisement, bool) {
	parts := strings.Split(d.Address, ":")
	if len(parts) != 6 {
		return Advertisement{}, false
	}
	var a Advertisement
	for i, p := range parts {
		b, err := hex.DecodeString(p)
		if err != nil || len(b) != 1 {
			return Advertisement{}, false
		}
		a.Address[5-i] = b[0] // least significant byte first, as the controller reports it
	}
	if d.AddressType == "random" {
		a.AddressType = 1
	}
	a.RSSI = int8(d.RSSI)
	var data []byte
	add := func(typ byte, payload []byte) {
		if len(payload)+1 > 255 {
			return
		}
		data = append(data, byte(len(payload)+1), typ)
		data = append(data, payload...)
	}
	var short []byte
	for _, u := range d.UUIDs {
		if v, ok := uuid16(u); ok {
			short = binary.LittleEndian.AppendUint16(short, v)
		}
	}
	if len(short) > 0 {
		add(0x03, short) // complete list of 16-bit service UUIDs
	}
	for u, sd := range d.ServiceData {
		if v, ok := uuid16(u); ok {
			add(0x16, append(binary.LittleEndian.AppendUint16(nil, v), sd...))
		} else if raw, err := uuid128(u); err == nil {
			add(0x21, append(raw, sd...))
		}
	}
	for company, md := range d.ManufacturerData {
		add(0xFF, append(binary.LittleEndian.AppendUint16(nil, company), md...))
	}
	if d.Name != "" {
		add(0x09, []byte(d.Name))
	}
	if len(data) == 0 {
		return Advertisement{}, false
	}
	a.Data = data
	return a, true
}

// uuid16 recognizes the Bluetooth base UUID form of a 16-bit service.
func uuid16(u string) (uint16, bool) {
	u = strings.ToLower(u)
	if len(u) != 36 || !strings.HasSuffix(u, "-0000-1000-8000-00805f9b34fb") || !strings.HasPrefix(u, "0000") {
		return 0, false
	}
	b, err := hex.DecodeString(u[4:8])
	if err != nil {
		return 0, false
	}
	return binary.BigEndian.Uint16(b), true
}

// uuid128 is a full UUID as the 16 little-endian bytes an advertisement carries.
func uuid128(u string) ([]byte, error) {
	b, err := hex.DecodeString(strings.ReplaceAll(u, "-", ""))
	if err != nil || len(b) != 16 {
		return nil, fmt.Errorf("ble: bad uuid %q", u)
	}
	for i, j := 0, 15; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return b, nil
}
