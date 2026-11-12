//go:build linux

package wallclock

import (
	"encoding/binary"
	"testing"
	"unsafe"
)

// TestSniffParams pins the HCI_Sniff_Mode payload: ten bytes, not the twelve a supervision timeout
// would make. Sniff_Mode has no supervision parameter; sending one makes the controller log an
// invalid packet size.
func TestSniffParams(t *testing.T) {
	p := sniffParams(0x0032)
	if len(p) != 10 {
		t.Fatalf("sniff params are %d bytes, want 10", len(p))
	}
	for _, tc := range []struct {
		at   int
		want uint16
	}{
		{0, 0x0032},           // handle
		{2, sniffMaxInterval}, // max interval
		{4, sniffMinInterval}, // min interval
		{6, sniffAttempt},     // attempt
		{8, sniffTimeout},     // timeout
	} {
		if got := binary.LittleEndian.Uint16(p[tc.at:]); got != tc.want {
			t.Errorf("param at %d = %d, want %d", tc.at, got, tc.want)
		}
	}
}

// TestHCIAddress pins the byte order the kernel uses for bdaddr_t: the text address is reversed.
func TestHCIAddress(t *testing.T) {
	got, err := hciAddress("7C:D5:66:BD:D1:61")
	if err != nil {
		t.Fatalf("hciAddress: %v", err)
	}
	want := [6]byte{0x61, 0xD1, 0xBD, 0x66, 0xD5, 0x7C}
	if got != want {
		t.Fatalf("hciAddress = % X, want % X", got, want)
	}
	if s := addressString(got); s != "7C:D5:66:BD:D1:61" {
		t.Fatalf("addressString = %q, want the original address", s)
	}
	for _, bad := range []string{"", "7C:D5:66:BD:D1", "not-a-mac"} {
		if _, err := hciAddress(bad); err == nil {
			t.Errorf("hciAddress(%q) accepted a bad address", bad)
		}
	}
}

// TestDecodeCommandReply pins the two reply shapes and the offsets the kernel uses.
func TestDecodeCommandReply(t *testing.T) {
	complete := []byte{0x04, 0x0E, 0x04, 0x01, 0x03, 0x08, 0x00}
	status := []byte{0x04, 0x0F, 0x04, 0x00, 0x01, 0x03, 0x08}

	for _, tc := range []struct {
		name   string
		pkt    []byte
		opcode uint16
		want   uint8
		ok     bool
	}{
		{"command complete", complete, 0x0803, 0x00, true},
		{"command status", status, 0x0803, 0x00, true},
		{"command complete, other opcode", complete, 0x0804, 0, false},
		{"command status, other opcode", status, 0x0804, 0, false},
		{"short packet", complete[:2], 0x0803, 0, false},
		{"short body", []byte{0x04, 0x0E, 0x02, 0x01, 0x03}, 0x0803, 0, false},
		{"not an event", append([]byte{0x02}, complete[1:]...), 0x0803, 0, false},
	} {
		got, ok := decodeCommandReply(tc.pkt, tc.opcode)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s: got (0x%02X, %v), want (0x%02X, %v)", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

// TestHCILayout pins the C struct sizes the ioctl writes into; a Go field change would otherwise
// shift every connection's handle without a compile error.
func TestHCILayout(t *testing.T) {
	if got := unsafe.Sizeof(hciConnInfo{}); got != 16 {
		t.Fatalf("hciConnInfo is %d bytes, want 16", got)
	}
	if got := unsafe.Sizeof(hciConnList{}); got != 4+16*hciConnMax {
		t.Fatalf("hciConnList is %d bytes, want %d", got, 4+16*hciConnMax)
	}
	if opSniffMode != 0x0803 {
		t.Fatalf("opSniffMode = 0x%04X, want 0x0803", opSniffMode)
	}
	if hciGetConnList != 0x800448D4 {
		t.Fatalf("hciGetConnList = 0x%08X, want 0x800448D4", hciGetConnList)
	}
	if hciLMMaster != 0x0001 {
		t.Fatalf("hciLMMaster = 0x%04X, want 0x0001", hciLMMaster)
	}
}
