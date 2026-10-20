//go:build linux

package facedetect

import (
	"encoding/binary"
	"image"
	"testing"
)

// The detector's first records for a public-domain portrait (640x480, the face at about 240,140 to
// 385,285), as the unit gave them.
var portrait = [][4]uint32{
	{0x459e2000, 0xf728e304, 0x0001c001, 0}, {0x489e2001, 0x66691304, 0x0001c001, 0},
	{0x4b9e2002, 0x84894304, 0x0001c001, 0}, {0x391ba003, 0xae294b28, 0x00020001, 0},
	{0x3d1aa004, 0xc3e98b18, 0x00020001, 0}, {0x3d1ba005, 0x37298b28, 0x00020002, 0},
}

func result(records [][4]uint32) []byte {
	out := make([]byte, fdResultSize*fdMaxFaces+2)
	for i, r := range records {
		for j, w := range r {
			binary.LittleEndian.PutUint32(out[i*fdResultSize+4*j:], w)
		}
	}
	binary.LittleEndian.PutUint16(out[fdResultSize*fdMaxFaces:], uint16(len(records)))
	return out
}

func TestParse(t *testing.T) {
	f := parse(result(portrait))
	if len(f) != len(portrait) {
		t.Fatalf("%d faces", len(f))
	}
	if want := image.Rect(241, 139, 386, 284); f[0].Box != want || f[0].Score != 1977 {
		t.Errorf("first: %+v, want %v score 1977", f[0], want)
	}
	if parse(nil) != nil {
		t.Error("a short result read as faces")
	}
}

func TestMergeOneFace(t *testing.T) {
	m := merge(parse(result(portrait)))
	if len(m) != 1 || m[0].Votes != len(portrait) || m[0].Score != 1977 {
		t.Fatalf("merged: %+v", m)
	}
	two := merge([]Face{{Box: image.Rect(0, 0, 50, 50), Score: 5, Votes: 1}, {Box: image.Rect(300, 300, 350, 350), Score: 9, Votes: 1}})
	if len(two) != 2 || two[0].Score != 9 {
		t.Fatalf("two faces apart: %+v", two)
	}
}
