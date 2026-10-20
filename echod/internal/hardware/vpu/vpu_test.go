//go:build linux

package vpu

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"
)

// Against a cam_vpu1 dump (VPU_CAM_VPU1) and the region tools/vpu/mkregion.py built from it (VPU_REGION).
func TestFirmwareMatchesMkregion(t *testing.T) {
	p, r := os.Getenv("VPU_CAM_VPU1"), os.Getenv("VPU_REGION")
	if p == "" || r == "" {
		t.Skip("VPU_CAM_VPU1, VPU_REGION not set")
	}
	part, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(r)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Firmware(part)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("differs from offset %#x", i)
			}
		}
	}
}

// Against a cam_vpu3 dump (VPU_CAM_VPU3): ksample is core 0's first algorithm, 0xaf12 bytes at 0xf38.
func TestAlgorithm(t *testing.T) {
	p := os.Getenv("VPU_CAM_VPU3")
	if p == "" {
		t.Skip("VPU_CAM_VPU3 not set")
	}
	part, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	elf, err := Algorithm(part, "vpu_flo_d2d_ksample")
	if err != nil {
		t.Fatal(err)
	}
	if len(elf) != 0xaf12 || !bytes.Equal(elf, part[0xf38:0xf38+0xaf12]) || string(elf[1:4]) != "ELF" {
		t.Fatalf("got %d bytes", len(elf))
	}
}

// A made-up partition: two algorithms in the table, the second core 0's; Algorithm finds it by name
// from the table alone (algorithmAt) and from the whole partition.
func TestAlgorithmTable(t *testing.T) {
	part := make([]byte, 0x4000)
	le := binary.LittleEndian
	tab := part[imageHdr+algoCountOff:]
	le.PutUint32(tab, 2)
	for i, a := range []struct {
		core, off, n uint32
		name         string
	}{{0x62, 0x2000, 16, "x"}, {core0, 0x3000, 32, "x"}} {
		e := tab[4+i*44:]
		le.PutUint32(e, a.core)
		le.PutUint32(e[4:], a.off)
		le.PutUint32(e[8:], a.n)
		copy(e[12:], a.name)
	}
	copy(part[imageHdr+0x3000:], "\x7fELF")
	off, n, err := algorithmAt(part[:tableLen], "x")
	if err != nil || off != imageHdr+0x3000 || n != 32 {
		t.Fatalf("at %#x, %d bytes: %v", off, n, err)
	}
	elf, err := Algorithm(part, "x")
	if err != nil || len(elf) != 32 || string(elf[:4]) != "\x7fELF" {
		t.Fatalf("%d bytes: %v", len(elf), err)
	}
	if _, err := Algorithm(part, "y"); err == nil {
		t.Fatal("found an algorithm that is not there")
	}
	if _, err := Algorithm(part[:imageHdr+0x3010], "x"); err == nil {
		t.Fatal("an ELF past the partition's end was taken")
	}
}

// A net is a chain: a conv that does not take what the one before gives is refused.
func TestPrepareNetChain(t *testing.T) {
	lm := LocalMem{Banks: 2, Contiguous: 1, Size: [2]uint32{124304, 118784}}
	conv := func(inC, outC int) *Conv {
		c := &Conv{InW: 8, InH: 8, InC: inC, OutC: outC, KW: 1, KH: 1, Stride: 1,
			InScale: 0.02, WScale: 0.004, OutScale: 0.05, InZero: 120, WZero: 131, OutZero: 100}
		c.Weights = make([]uint8, outC*inC)
		c.Bias = make([]int32, outC)
		return c
	}
	a, b := conv(8, 16), conv(16, 8)
	b.InScale, b.InZero = a.OutScale, a.OutZero
	n, err := PrepareNet([]*Conv{a, b}, lm)
	if err != nil {
		t.Fatal(err)
	}
	if n.OutLen() != 8*8*8 || len(n.ops) != 2 || len(n.tensors) != 7 {
		t.Fatalf("out %d, %d ops, %d tensors", n.OutLen(), len(n.ops), len(n.tensors))
	}
	for _, x := range n.tensors {
		if x.Offset < ReservedData || x.Offset%64 != 0 {
			t.Fatalf("a tensor at %#x", x.Offset)
		}
	}
	if _, err := PrepareNet([]*Conv{a, conv(8, 8)}, lm); err == nil {
		t.Fatal("a broken chain was taken")
	}
}
