//go:build linux && hwdevice

package vpu

import (
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"
)

// openAPUNN boots the unit's firmware and loads apunn.
func openAPUNN(t *testing.T) *Device {
	t.Helper()
	if !Available() {
		t.Skip("no " + Path)
	}
	if v, err := strconv.Atoi(os.Getenv("VPU_CMDBUFS")); err == nil {
		cmdBufs = v
	}
	began := time.Now()
	img, err := ReadFirmware()
	if err != nil {
		t.Fatal(err)
	}
	elf, err := ReadAlgorithm("cam_vpu2", APUNN)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("partitions read in %s", time.Since(began).Round(time.Microsecond))
	d, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	began = time.Now()
	if err := d.Boot(img); err != nil {
		t.Fatal(err)
	}
	booted := time.Now()
	if err := d.Load(elf); err != nil {
		t.Fatal(err)
	}
	t.Logf("booted in %s, apunn loaded in %s", booted.Sub(began).Round(time.Microsecond), time.Since(booted).Round(time.Microsecond))
	return d
}

func TestDeviceAPUNNVersion(t *testing.T) {
	d := openAPUNN(t)
	v, err := d.Version()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("version %q", v)
	if v != "XTENSA_ANN_V1" {
		t.Fatal("not the version the HAL wants")
	}
	lm, err := d.LocalMem()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("local memory %+v", lm)
	began := time.Now()
	for range 100 {
		if _, err := d.Version(); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("a command that does nothing: %s", time.Since(began)/100)
}

// testConv is a random conv with NNAPI 1.1's quantization.
func testConv(depthwise bool, w, h, cin, cout, k, stride int, act int, seed int64) (*Conv, []uint8) {
	r := rand.New(rand.NewSource(seed))
	pad := k / 2
	c := &Conv{Depthwise: depthwise, InW: w, InH: h, InC: cin, OutC: cout, KW: k, KH: k, Stride: stride,
		PadL: pad, PadR: pad, PadT: pad, PadB: pad,
		InScale: 0.02, WScale: 0.004, OutScale: 0.05, InZero: 120, WZero: 131, OutZero: 100, Act: act}
	if v, err := strconv.Atoi(os.Getenv("VPU_KIND")); err == nil {
		c.Kind = v
	}
	nw := cout * k * k * cin
	if depthwise {
		nw = k * k * cout
	}
	c.Weights = make([]uint8, nw)
	for i := range c.Weights {
		c.Weights[i] = uint8(r.Intn(256))
	}
	c.Bias = make([]int32, cout)
	for i := range c.Bias {
		c.Bias[i] = int32(r.Intn(4001) - 2000)
	}
	in := make([]uint8, w*h*cin)
	for i := range in {
		in[i] = uint8(r.Intn(256))
	}
	return c, in
}

func runConv(t *testing.T, d *Device, c *Conv, in []uint8) {
	t.Helper()
	lm, err := d.LocalMem()
	if err != nil {
		t.Fatal(err)
	}
	pr, err := c.Prepare(lm)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("kind %d, tile %v, params % x", pr.Kind, pr.Tile, pr.Params)
	const inOff, outOff, constOff = ReservedData, 0x80000, 0x100000
	data := d.Data()
	copy(data[inOff:], in)
	want := c.Reference(in)
	for i := range len(want) + 256 {
		data[outOff+i] = 0xaa
	}
	var best time.Duration
	for n := 0; n < 5; n++ {
		began := time.Now()
		if err := d.Conv(pr, inOff, uint32(len(in)), outOff, uint32(len(want)), constOff, 2000); err != nil {
			t.Fatal(err)
		}
		if el := time.Since(began); n == 0 || el < best {
			best = el
		}
	}
	got := data[outOff : outOff+len(want)]
	diff, worst := 0, 0
	for i := range want {
		if e := int(got[i]) - int(want[i]); e != 0 {
			diff++
			worst = max(worst, e, -e)
			if diff <= 8 {
				t.Logf("out[%d] (y %d x %d c %d): %d, want %d", i, i/c.OutC/c.OutW(), i/c.OutC%c.OutW(), i%c.OutC, got[i], want[i])
			}
		}
	}
	past := 0
	for _, b := range data[outOff+len(want) : outOff+len(want)+256] {
		if b != 0xaa {
			past++
		}
	}
	t.Logf("%d of %d outputs differ (worst by %d), %d bytes written past it; best of 5: %s", diff, len(want), worst, past, best)
	if worst > 1 || past != 0 {
		t.Fatal("not the reference")
	}
}

func TestDeviceAPUNNConv(t *testing.T) {
	d := openAPUNN(t)
	c, in := testConv(false, 8, 8, 8, 8, 3, 1, 0, 1)
	runConv(t, d, c, in)
}

func TestDeviceAPUNNConv1x1(t *testing.T) {
	d := openAPUNN(t)
	c, in := testConv(false, 12, 12, 32, 64, 1, 1, 3, 2)
	runConv(t, d, c, in)
}

func TestDeviceAPUNNDepthwise(t *testing.T) {
	d := openAPUNN(t)
	c, in := testConv(true, 16, 16, 16, 16, 3, 1, 3, 3)
	runConv(t, d, c, in)
}

// A chain of convolutions as one command (Net), against the same chain on the CPU, byte for byte.
func TestDeviceNet(t *testing.T) {
	d := openAPUNN(t)
	lm, err := d.LocalMem()
	if err != nil {
		t.Fatal(err)
	}
	a, in := testConv(false, 24, 24, 8, 16, 3, 2, 3, 7)
	b, _ := testConv(true, 12, 12, 16, 16, 3, 1, 3, 8)
	c, _ := testConv(false, 12, 12, 16, 32, 1, 1, 3, 9)
	b.InScale, b.InZero, c.InScale, c.InZero = a.OutScale, a.OutZero, b.OutScale, b.OutZero
	n, err := PrepareNet([]*Conv{a, b, c}, lm)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Place(n); err != nil {
		t.Fatal(err)
	}
	want := c.Reference(b.Reference(a.Reference(in)))
	got := make([]byte, n.OutLen())
	began := time.Now()
	if err := d.RunNet(n, in, got, 1000); err != nil {
		t.Fatal(err)
	}
	t.Logf("3 convs in %s", time.Since(began))
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("out[%d] %d, want %d", i, got[i], want[i])
		}
	}
}

// Layers too big for the local memory: tiled by the HAL's planner.
func TestDeviceAPUNNTiled(t *testing.T) {
	d := openAPUNN(t)
	for _, tc := range []struct {
		dw                    bool
		w, h, cin, cout, k, s int
	}{
		{false, 64, 64, 32, 64, 1, 1},
		{false, 40, 40, 16, 96, 3, 1},
		{false, 64, 48, 24, 24, 3, 2},
		{true, 64, 64, 64, 64, 3, 1},
		{true, 96, 80, 48, 48, 3, 2},
	} {
		c, in := testConv(tc.dw, tc.w, tc.h, tc.cin, tc.cout, tc.k, tc.s, 3, int64(tc.w*tc.cout))
		runConv(t, d, c, in)
	}
}
