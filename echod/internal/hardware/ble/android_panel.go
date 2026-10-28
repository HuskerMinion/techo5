//go:build panel

package ble

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/panelsock"
)

// The Panel app owns Android's BluetoothLeScanner. The daemon gets its raw scan reports over an
// abstract Unix socket that both ends verify belongs to this app's uid. The bridge carries passive
// advertisements only; Android does not expose the controller or a public address-type API on 8.1.
const panelSocket = "@techo5.ble"

type panelScanStatus byte

func (s panelScanStatus) Error() string {
	return fmt.Sprintf("Android BLE scan rejected: %d", s)
}

func (s panelScanStatus) Is(target error) bool {
	// The app reserves 1 for missing permission and 2 for a disabled adapter.
	return target == ErrScanUnavailable && (s == 1 || s == 2)
}

type androidScanner struct {
	mu       sync.Mutex
	conn     net.Conn
	running  bool
	scanning bool
	reports  atomic.Uint64
}

var panelRadio = &androidScanner{}

func Default() Scanner { return panelRadio }

func (s *androidScanner) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *androidScanner) Scanning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scanning
}

func (s *androidScanner) Reports() uint64 { return s.reports.Load() }

func (s *androidScanner) Start(scan, active bool, advertisement []byte, found func(Advertisement)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return nil
	}
	if len(advertisement) != 0 {
		return errors.New("ble: Android Panel cannot transmit beacon advertisements")
	}
	if !scan {
		return nil
	}
	c, err := panelsock.Dial(panelSocket)
	if err != nil {
		return fmt.Errorf("ble: panel socket: %w", err)
	}
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	mode := byte(0)
	if active {
		mode = 1
	}
	if _, err = c.Write([]byte{mode}); err == nil {
		var status [1]byte
		_, err = io.ReadFull(c, status[:])
		if err == nil && status[0] != 0 {
			err = panelScanStatus(status[0])
		}
	}
	if err != nil {
		_ = c.Close()
		return fmt.Errorf("ble: panel scan: %w", err)
	}
	_ = c.SetDeadline(time.Time{})
	s.conn, s.running, s.scanning = c, true, true
	go s.read(c, found)
	return nil
}

func (s *androidScanner) Stop() {
	s.mu.Lock()
	c := s.conn
	s.conn, s.running, s.scanning = nil, false, false
	s.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

func (s *androidScanner) read(c net.Conn, found func(Advertisement)) {
	defer func() {
		s.mu.Lock()
		if s.conn == c {
			s.conn, s.running, s.scanning = nil, false, false
		}
		s.mu.Unlock()
		_ = c.Close()
	}()
	for {
		var size [2]byte
		if _, err := io.ReadFull(c, size[:]); err != nil {
			return
		}
		n := int(binary.LittleEndian.Uint16(size[:]))
		if n < 8 || n > 520 {
			return
		}
		frame := make([]byte, n)
		if _, err := io.ReadFull(c, frame); err != nil {
			return
		}
		var a Advertisement
		// Android prints addresses most-significant byte first; HCI's Advertisement holds the
		// reverse order, and Addr() returns Home Assistant's usual integer.
		for i := 0; i < 6; i++ {
			a.Address[i] = frame[5-i]
		}
		a.AddressType, a.RSSI = frame[6], int8(frame[7])
		a.Data = frame[8:]
		s.reports.Add(1)
		found(a)
	}
}

var _ Scanner = (*androidScanner)(nil)
