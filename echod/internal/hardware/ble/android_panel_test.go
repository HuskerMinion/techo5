//go:build panel

package ble

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestPanelScanRefusalAndRecovery(t *testing.T) {
	listener, err := net.Listen("unix", panelSocket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	s := &androidScanner{}
	defer s.Stop()
	for _, status := range []byte{1, 2, 3, 0} {
		closed := make(chan error, 1)
		go func() {
			c, err := listener.Accept()
			if err != nil {
				closed <- err
				return
			}
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(time.Second))
			var mode [1]byte
			if _, err := io.ReadFull(c, mode[:]); err != nil {
				closed <- err
				return
			}
			if mode[0] != 1 {
				closed <- errors.New("active scan mode was not forwarded")
				return
			}
			if _, err := c.Write([]byte{status}); err != nil {
				closed <- err
				return
			}
			_, err = io.Copy(io.Discard, c)
			closed <- err
		}()
		err := s.Start(true, true, nil, func(Advertisement) {})
		if errors.Is(err, ErrScanUnavailable) != (status == 1 || status == 2) {
			t.Fatalf("status %d: unavailable classification: %v", status, err)
		}
		if (err == nil) != (status == 0) || s.Running() != (status == 0) || s.Scanning() != (status == 0) {
			t.Fatalf("status %d: running=%t scanning=%t err=%v", status, s.Running(), s.Scanning(), err)
		}
		s.Stop()
		select {
		case err := <-closed:
			if err != nil {
				t.Fatalf("status %d: socket was not closed: %v", status, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("scan socket remained open")
		}
	}
}

func TestPanelReportPreservesAddressRSSIAndPayload(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	s := &androidScanner{conn: client, running: true, scanning: true}
	got := make(chan Advertisement, 1)
	go s.read(client, func(a Advertisement) { got <- a })
	payload := []byte{2, 1, 6}
	frame := append([]byte{2, 0, 0, 0, 0, 1, 1, 0xc4}, payload...)
	header := make([]byte, 2)
	binary.LittleEndian.PutUint16(header, uint16(len(frame)))
	go func() { _, _ = server.Write(append(header, frame...)) }()
	select {
	case a := <-got:
		if a.Addr() != 0x020000000001 || a.RSSI != -60 || a.AddressType != 1 || string(a.Data) != string(payload) {
			t.Fatalf("report: %+v", a)
		}
	case <-time.After(time.Second):
		t.Fatal("no scan report")
	}
	if s.Reports() != 1 {
		t.Fatal("report counter")
	}
	_ = server.Close()
}
