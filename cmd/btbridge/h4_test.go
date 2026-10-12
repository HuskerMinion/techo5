package main

import (
	"bytes"
	"testing"
)

var (
	evt = []byte{0x04, 0x0e, 0x04, 0x01, 0x03, 0x0c, 0x00}                   // Command Complete, HCI_Reset
	acl = []byte{0x02, 0x01, 0x20, 0x05, 0x00, 1, 2, 3, 4, 5}                // 5-byte ACL payload
	sco = []byte{0x03, 0x01, 0x00, 0x02, 0xaa, 0xbb}                         // 2-byte SCO payload
	big = append([]byte{0x02, 0x01, 0x20, 0x00, 0x01}, make([]byte, 256)...) // length 0x0100
)

func join(ps ...[]byte) []byte { return bytes.Join(ps, nil) }

func check(t *testing.T, got [][]byte, want ...[]byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d packets, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Errorf("packet %d: got % x, want % x", i, got[i], want[i])
		}
	}
}

// The Show's driver: one whole packet per read.
func TestWholePacketsPassThrough(t *testing.T) {
	var f h4Framer
	check(t, f.feed(evt), evt)
	check(t, f.feed(acl), acl)
	check(t, f.feed(sco), sco)
}

// The Dot's driver: several packets in one read.
func TestSeveralPacketsInOneRead(t *testing.T) {
	var f h4Framer
	check(t, f.feed(join(evt, acl, sco, big)), evt, acl, sco, big)
}

// And packets split across reads anywhere, including inside the header.
func TestPacketsSplitAnywhere(t *testing.T) {
	stream := join(evt, big, acl, sco)
	for cut := 1; cut < len(stream); cut++ {
		var f h4Framer
		got := f.feed(stream[:cut])
		got = append(got, f.feed(stream[cut:])...)
		check(t, got, evt, big, acl, sco)
	}
	// One byte at a time.
	var f h4Framer
	var got [][]byte
	for _, b := range stream {
		got = append(got, f.feed([]byte{b})...)
	}
	check(t, got, evt, big, acl, sco)
}

// Lengths whose low and high bytes both carry bits (0x01fc = 508), where + and | would disagree.
func TestTwoByteLengths(t *testing.T) {
	long := append([]byte{0x02, 0x01, 0x20, 0xfc, 0x01}, make([]byte, 0x01fc)...)
	iso := append([]byte{0x05, 0x01, 0x00, 0xfc, 0x41}, make([]byte, 0x01fc)...) // flag bits set in the top byte
	var f h4Framer
	check(t, f.feed(join(long, evt, iso, sco)), long, evt, iso, sco)
}

// The Dot's controller claims page 2 of its extended features and refuses to read it: its replies
// say page 1 is the last, and nothing else is touched.
func TestCapFeaturePages(t *testing.T) {
	page0 := []byte{0x04, 0x0e, 0x0e, 0x01, 0x04, 0x10, 0x00, 0x00, 0x02, 1, 2, 3, 4, 5, 6, 7, 8}
	if !capFeaturePages(page0, 1) || page0[8] != 1 {
		t.Errorf("max page not capped: % x", page0)
	}
	if page0[7] != 0 || page0[9] != 1 || page0[16] != 8 {
		t.Errorf("more than the max page changed: % x", page0)
	}
	failed := []byte{0x04, 0x0e, 0x0e, 0x01, 0x04, 0x10, 0x30, 0x02, 0x02, 0, 0, 0, 0, 0, 0, 0, 0}
	if capFeaturePages(failed, 1) {
		t.Error("a failed reply was changed")
	}
	version := []byte{0x04, 0x0e, 0x0c, 0x01, 0x01, 0x10, 0x00, 0x06, 0x20, 0x17, 0x08, 0x46, 0x00, 0x18, 0x04}
	if capFeaturePages(version, 1) {
		t.Error("another command's reply was changed")
	}
	if capFeaturePages(evt, 1) {
		t.Error("an unrelated event was changed")
	}
}

// Packets handed out earlier are not overwritten by later reads.
func TestPacketsStayIntact(t *testing.T) {
	var f h4Framer
	first := f.feed(join(evt, acl[:3]))
	f.feed(join(acl[3:], sco))
	check(t, first, evt)
}

// Garbage before a packet is skipped and counted, and framing recovers.
func TestResyncsAfterGarbage(t *testing.T) {
	var f h4Framer
	check(t, f.feed(join([]byte{0x00, 0xff}, evt)), evt)
	if f.dropped != 2 {
		t.Errorf("dropped %d bytes, want 2", f.dropped)
	}
}

// A phone's P-256 link key reaches the kernel as the P-192 kind, authenticated or not as it was, and
// nothing else is touched: other key types, other events, a short packet.
func TestP256KeysArePassedOnAsP192(t *testing.T) {
	note := func(kind byte) []byte {
		p := []byte{h4Event, 0x18, 0x17}
		p = append(p, make([]byte, 6+16)...)
		return append(p, kind)
	}
	for _, c := range []struct{ in, want byte }{{8, 5}, {7, 4}, {5, 5}, {4, 4}, {0, 0}, {6, 6}} {
		p := note(c.in)
		changed := p192Key(p)
		if p[25] != c.want || changed != (c.in != c.want) {
			t.Errorf("type %d: became %d, changed %v; want %d", c.in, p[25], changed, c.want)
		}
	}
	other := note(8)
	other[1] = 0x0e
	if p192Key(other) || other[25] != 8 {
		t.Error("a packet that is not a Link Key Notification was changed")
	}
	if p192Key(note(8)[:20]) {
		t.Error("a short packet was changed")
	}
}

func TestAcceptedAsPeripheral(t *testing.T) {
	accept := func(role byte) []byte {
		return []byte{h4Command, 0x09, 0x04, 0x07, 1, 2, 3, 4, 5, 6, role}
	}
	for _, c := range []struct{ in, want byte }{{0, 1}, {1, 1}} {
		p := accept(c.in)
		changed := acceptAsPeripheral(p)
		if p[10] != c.want || changed != (c.in != c.want) {
			t.Errorf("role %d: became %d, changed %v; want %d", c.in, p[10], changed, c.want)
		}
	}
	reject := accept(0)
	reject[1] = 0x0a // Reject Connection Request: the same length, the last byte a reason
	if acceptAsPeripheral(reject) || reject[10] != 0 {
		t.Error("a packet that is not an Accept Connection Request was changed")
	}
	if acceptAsPeripheral(accept(0)[:9]) {
		t.Error("a short packet was changed")
	}
}
