//go:build panel

package ble

import (
	"encoding/binary"
	"net"
	"testing"
	"time"
)

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
