//go:build linux && hwdevice

package vpu

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

// On a Show 8 with the VPU driver: the unit's firmware booted, its sample algorithm loaded, and a
// 64x64 copy through the VPU (in by its DMA, through the core, out by its DMA) checked byte for byte.
func TestDeviceCopy(t *testing.T) {
	if !Available() {
		t.Skip("no " + Path)
	}
	fw, err := ReadPartition("cam_vpu1")
	if err != nil {
		t.Fatal(err)
	}
	img, err := Firmware(fw)
	if err != nil {
		t.Fatal(err)
	}
	algos, err := ReadPartition("cam_vpu3")
	if err != nil {
		t.Fatal(err)
	}
	elf, err := Algorithm(algos, "vpu_flo_d2d_ksample")
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	began := time.Now()
	if err := d.Boot(img); err != nil {
		t.Fatal(err)
	}
	t.Logf("booted in %s", time.Since(began).Round(time.Millisecond))
	if err := d.Load(elf); err != nil {
		t.Fatal(err)
	}
	const n = 64 * 64
	data := d.Data()
	for i := 0; i < n; i++ {
		data[i] = byte(i*7 + i/64)
	}
	for i := 0; i < 2*n; i++ {
		data[0x10000+i] = 0xaa
	}
	settings := make([]byte, 8)
	binary.LittleEndian.PutUint32(settings, 64)
	binary.LittleEndian.PutUint32(settings[4:], 64)
	began = time.Now()
	stage, err := d.Run([]Buf{
		{Port: 0, Format: FormatY8, Width: 64, Height: 64, Stride: 64, Length: n, Offset: 0},
		{Port: 1, Format: FormatY8, Width: 64, Height: 64, Stride: 64, Length: n, Offset: 0x10000},
	}, settings, 1000)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("job in %s, stage %#x", time.Since(began).Round(time.Microsecond), stage)
	if !bytes.Equal(data[0x10000:0x10000+n], data[:n]) {
		t.Fatal("the copy differs from the input")
	}
	for _, b := range data[0x10000+n : 0x10000+2*n] {
		if b != 0xaa {
			t.Fatal("the job wrote past its output")
		}
	}
}
