//go:build !dot && !spot && !panel

package camera

import (
	"os"
	"path/filepath"
	"testing"
)

// useSensor runs a test with the package set up for another sensor, as boardSensor would on that
// board, and puts the build host's back afterwards.
func useSensor(t *testing.T, s sensorSpec) {
	t.Helper()
	was := sensor
	set := func(s sensorSpec) {
		sensor = s
		Width, Height = s.outW, s.outH
		sensorW, sensorH = s.w, s.h
		bytesPerLine = sensorW * 10 / 8
		frameBytes = bytesPerLine * sensorH
		aeMinShut, aeFrame, aeMaxShut = s.minShut, s.frameLines, s.maxShut
		blackCap = s.blackCap
	}
	set(s)
	t.Cleanup(func() { set(was) })
}

// bayerFrame is a BGGR frame (athena's order) whose every cell reads r, g, b above the pedestal.
func bayerFrame(cell func(x, y int) (r, g, b uint16)) []byte {
	ped := uint16(sensor.pedestal)
	return pack(func(x, y int) uint16 {
		r, g, b := cell(x/2, y/2)
		switch {
		case y%2 == 0 && x%2 == 0:
			return b + ped
		case y%2 == 1 && x%2 == 1:
			return r + ped
		}
		return g + ped
	})
}

func TestAthenaSensor(t *testing.T) {
	s := athenaSensor
	if !s.v4l2 || !s.blueFirst || s.pedestal != 64 {
		t.Errorf("%+v: V4L2, BGGR, a black level of 64", s)
	}
	if s.outW*2 != s.w || s.outH*2 != s.h || s.w*10%8 != 0 || s.w*10/8%16 != 0 {
		t.Errorf("%+v: half-size live view, and a packed line CAMSV can write (16-byte steps)", s)
	}
	if s.maxShut > s.frameLines || s.blackCap < 2*s.pedestal {
		t.Errorf("%+v: the shutter stays in a frame, and black covers the pedestal", s)
	}
}

// A frame lit red-heavy above the OV13855's pedestal is balanced to gray: with the pedestal left in
// the gains came out near 1 and the cast stayed.
func TestAthenaWhiteBalanceTakesThePedestalOff(t *testing.T) {
	useSensor(t, athenaSensor)
	raw := bayerFrame(func(int, int) (uint16, uint16, uint16) { return 80, 40, 40 })
	tn := stats(raw)
	// Summed green 80 against red 80 and blue 40, each doubled to the summed scale: red 1/2, blue 1.
	if tn.gainR < 0.45 || tn.gainR > 0.55 || tn.gainB < 0.95 || tn.gainB > 1.05 {
		t.Errorf("gains r %.3f b %.3f, want about 0.5 and 1", tn.gainR, tn.gainB)
	}
	if tn.black < 2*athenaSensor.pedestal-4 {
		t.Errorf("black %d: the pedestal (128 summed) should all go", tn.black)
	}
}

// Color bars in BGGR come out in their colors, so red and blue are not swapped, on the live view,
// the full still and the luma grid alike.
func TestAthenaColorBars(t *testing.T) {
	useSensor(t, athenaSensor)
	bars := [][3]uint16{{900, 900, 900}, {900, 0, 0}, {0, 900, 0}, {0, 0, 900}} // white, red, green, blue
	raw := bayerFrame(func(x, y int) (uint16, uint16, uint16) {
		c := bars[x*len(bars)/(sensorW/2)]
		return c[0], c[1], c[2]
	})
	// Leveled as a gray frame would be, so the bars keep their colors.
	tn := tone{gainR: 1, gainB: 1, white: 1800 + 2*athenaSensor.pedestal, black: 2 * athenaSensor.pedestal, gamma: gammaNormal}
	img := render(raw, tn)
	if b := img.Bounds(); b.Dx() != 1056 || b.Dy() != 784 {
		t.Fatalf("live view %v, want 1056x784", b)
	}
	f := &Frame{raw: raw, tone: tn, toned: true}
	full := f.Full()
	if b := full.Bounds(); b.Dx() != 2112 || b.Dy() != 1568 {
		t.Fatalf("still %v, want 2112x1568", b)
	}
	for i, want := range []string{"white", "red", "green", "blue"} {
		x := (2*i + 1) * Width / (2 * len(bars))
		for _, p := range []struct {
			name    string
			r, g, b uint8
		}{
			{"live", img.RGBAAt(x, Height/2).R, img.RGBAAt(x, Height/2).G, img.RGBAAt(x, Height/2).B},
			{"still", full.RGBAAt(2*x, sensorH/2).R, full.RGBAAt(2*x, sensorH/2).G, full.RGBAAt(2*x, sensorH/2).B},
		} {
			got := colorName(p.r, p.g, p.b)
			if got != want {
				t.Errorf("%s bar %d: rgb %d %d %d reads %s, want %s", p.name, i, p.r, p.g, p.b, got, want)
			}
		}
	}
	if g := f.Luma(32, 24); len(g) != 32*24 || g[0] < 200 {
		t.Errorf("luma grid: %d cells, white corner %v", len(g), g[:1])
	}
}

func colorName(r, g, b uint8) string {
	hi := func(v uint8) bool { return v > 160 }
	lo := func(v uint8) bool { return v < 60 }
	switch {
	case hi(r) && hi(g) && hi(b):
		return "white"
	case hi(r) && lo(g) && lo(b):
		return "red"
	case lo(r) && hi(g) && lo(b):
		return "green"
	case lo(r) && lo(g) && hi(b):
		return "blue"
	}
	return "other"
}

// The chain is found by its names in sysfs, and a missing sensor says why.
func TestFindChain(t *testing.T) {
	dir := t.TempDir()
	was := v4l2Sysfs
	v4l2Sysfs = dir
	t.Cleanup(func() { v4l2Sysfs = was })
	node := func(n, name string) {
		if err := os.MkdirAll(filepath.Join(dir, n), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, n, "name"), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	node("v4l-subdev0", "1a040000.seninf")
	node("v4l-subdev2", "1a050510.camsv")
	node("video0", "1a050510.camsv video stream")
	if _, err := findChain(); err == nil {
		t.Error("no sensor: want an error")
	}
	node("v4l-subdev1", "ov13855 2-0036")
	c, err := findChain()
	if err != nil {
		t.Fatal(err)
	}
	if c.sensor != "/dev/v4l-subdev1" || c.seninf != "/dev/v4l-subdev0" || c.camsv != "/dev/v4l-subdev2" || c.video != "/dev/video0" {
		t.Errorf("%+v", c)
	}
}

// The compat structures' sizes are what the kernel's 32-bit layer expects, and the 64-bit ones the
// native kernel's.
func TestV4L2Layout(t *testing.T) {
	l := v4l2Layout()
	switch wordSize {
	case 4:
		if l.size != 68 || l.planeSize != 60 || l.mOff != 52 {
			t.Errorf("32-bit %+v", l)
		}
	case 8:
		if l.size != 88 || l.planeSize != 64 || l.mOff != 64 {
			t.Errorf("64-bit %+v", l)
		}
	}
}
