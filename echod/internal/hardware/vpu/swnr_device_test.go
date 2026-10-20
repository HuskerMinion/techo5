//go:build linux && hwdevice

package vpu

import (
	"bytes"
	"fmt"
	"math"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// nrFromEnv is HW_NR ("K,Strength,BlendW,BlendTh"), or def.
func nrFromEnv(def NRParams) NRParams {
	f := strings.Split(os.Getenv("HW_NR"), ",")
	if len(f) != 4 {
		return def
	}
	var v [4]int
	for i, s := range f {
		v[i], _ = strconv.Atoi(s)
	}
	return NRParams{K: v[0], Strength: v[1], BlendW: v[2], BlendTh: v[3]}
}

func openSWNR(t *testing.T, w, h int) (*Device, *Chroma) {
	t.Helper()
	if !Available() {
		t.Skip("no " + Path)
	}
	img, err := ReadFirmware()
	if err != nil {
		t.Fatal(err)
	}
	d, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	if err := d.Boot(img); err != nil {
		t.Fatal(err)
	}
	c, err := d.NewChroma(w, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	return d, c
}

// On a Show 8 with the VPU driver: swnr on a made-up 1456x1088 picture's chroma (smooth ramps, a few
// sharp-edged squares, Gaussian noise of σ 4), the error against the clean planes before and after
// (HW_NR adds a tuning), and nothing written below its part of the data area.
func TestDeviceSWNRSynthetic(t *testing.T) {
	const W, H = 1456, 1088
	d, c := openSWNR(t, W, H)
	data := d.Data()
	for i := range data[:c.base] {
		data[i] = 0xaa
	}
	w, h := W/2, H/2
	clean := [2][]byte{make([]byte, w*h), make([]byte, w*h)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			u := 128 + 30*math.Sin(float64(x)/90) + 20*math.Cos(float64(y)/70)
			v := 128 - 25*math.Sin(float64(y)/60) + 15*math.Cos(float64(x+y)/110)
			if (x/96+y/96)%3 == 0 && x%96 > 16 && y%96 > 16 {
				u, v = u+40, v-35
			}
			clean[0][y*w+x], clean[1][y*w+x] = byte(u), byte(v)
		}
	}
	rng := rand.New(rand.NewSource(1))
	noisy := [2][]byte{make([]byte, w*h), make([]byte, w*h)}
	for p := range noisy {
		for i, v := range clean[p] {
			noisy[p][i] = byte(min(max(float64(v)+4*rng.NormFloat64()+0.5, 0), 255))
		}
	}
	rmse := func(a, b []byte) float64 {
		s := 0.0
		for i := range a {
			e := float64(a[i]) - float64(b[i])
			s += e * e
		}
		return math.Sqrt(s / float64(len(a)))
	}
	t.Logf("noisy: U %.2f V %.2f", rmse(noisy[0], clean[0]), rmse(noisy[1], clean[1]))
	for _, p := range []NRParams{nrFromEnv(NRStrongest), {K: 300, Strength: 60, BlendW: 32, BlendTh: 5},
		{K: 300, Strength: 60, BlendW: 16, BlendTh: 8}, {K: 600, Strength: 100, BlendW: 16, BlendTh: 8}} {
		u, v := c.Planes()
		copy(u, noisy[0])
		copy(v, noisy[1])
		began := time.Now()
		if err := c.Planar(p, 1000); err != nil {
			t.Fatal(err)
		}
		el := time.Since(began)
		t.Logf("%+v: U %.2f V %.2f in %s", p, rmse(u, clean[0]), rmse(v, clean[1]), el.Round(10*time.Microsecond))
		// the strongest here must take most of the noise out of both planes (a plane off a 4 KiB
		// boundary comes out mangled: §18.3)
		if p.K == 600 && (rmse(u, clean[0]) > 2 || rmse(v, clean[1]) > 2) {
			t.Fatal("not smoothed")
		}
	}
	for i, b := range data[:c.base] {
		if b != 0xaa {
			t.Fatalf("swnr wrote below its part, at %#x", i)
		}
	}
}

// On a Show 8 with the VPU driver: swnr on an NV12 picture (HW_NV12, HW_W x HW_H, default 1456x1088),
// written to HW_OUT, with HW_NR's tuning (Fire OS's strongest by default), timed over HW_REPS runs.
func TestDeviceSWNRFile(t *testing.T) {
	in, out := os.Getenv("HW_NV12"), os.Getenv("HW_OUT")
	if in == "" || out == "" {
		t.Skip("HW_NV12, HW_OUT not set")
	}
	W, H := 1456, 1088
	if v, err := strconv.Atoi(os.Getenv("HW_W")); err == nil {
		W = v
	}
	if v, err := strconv.Atoi(os.Getenv("HW_H")); err == nil {
		H = v
	}
	pic, err := os.ReadFile(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(pic) < W*H*3/2 {
		t.Fatalf("%d bytes, not a %dx%d NV12", len(pic), W, H)
	}
	_, c := openSWNR(t, W, H)
	p := nrFromEnv(NRStrongest)
	reps := 1
	if v, err := strconv.Atoi(os.Getenv("HW_REPS")); err == nil && v > 0 {
		reps = v
	}
	orig := append([]byte(nil), pic[W*H:W*H*3/2]...)
	var best, total time.Duration
	for i := 0; i < reps; i++ {
		uv := pic[W*H : W*H*3/2]
		copy(uv, orig)
		began := time.Now()
		if err := c.NV12(uv, W, p, 1000); err != nil {
			t.Fatal(err)
		}
		el := time.Since(began)
		total += el
		if best == 0 || el < best {
			best = el
		}
	}
	t.Logf("%+v: best %s, mean %s a picture", p, best.Round(10*time.Microsecond), (total / time.Duration(reps)).Round(10*time.Microsecond))
	if err := os.WriteFile(out, pic, 0o644); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stderr, "wrote", out)
}

// On a Show 8 with the VPU driver: NV12 through the data area's cached view gives the same bytes as
// through the uncached one (the kernel's cache maintenance around the jobs is right), picture after
// picture, for tunings from Fire OS's strongest to echod's at the sensor's ceiling, and the time of each.
func TestDeviceSWNRCachedSame(t *testing.T) {
	const W, H = 1456, 1088
	d, c := openSWNR(t, W, H)
	if !c.cached {
		t.Skip("no cached view of the data area (an older kernel)")
	}
	w, h := W/2, H/2
	rng := rand.New(rand.NewSource(3))
	pics := make([][]byte, 4)
	for k := range pics {
		pics[k] = make([]byte, W*h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				u := 128 + 30*math.Sin(float64(x+40*k)/90) + 20*math.Cos(float64(y)/70)
				v := 128 - 25*math.Sin(float64(y-30*k)/60) + 15*math.Cos(float64(x+y)/110)
				if (x/96+y/96+k)%3 == 0 && x%96 > 16 && y%96 > 16 {
					u, v = u+40, v-35
				}
				pics[k][y*W+2*x] = byte(min(max(u+10*rng.NormFloat64()+0.5, 0), 255))
				pics[k][y*W+2*x+1] = byte(min(max(v+10*rng.NormFloat64()+0.5, 0), 255))
			}
		}
	}
	tunings := []NRParams{NRStrongest, {K: 600, Strength: 100, BlendW: 24, BlendTh: 15},
		{K: 800, Strength: 350, BlendW: 16, BlendTh: 38}, {K: 1000, Strength: 600, BlendW: 8, BlendTh: 60}}
	run := func(cached bool) (outs [][]byte, took time.Duration) {
		c.planes, c.cached, c.buf, c.tabled = d.Data(), false, nil, false
		if cached {
			c.planes, c.cached = d.Cached(), true
		}
		for _, p := range tunings {
			for _, pic := range pics {
				uv := append([]byte(nil), pic...)
				began := time.Now()
				if err := c.NV12(uv, W, p, 1000); err != nil {
					t.Fatal(err)
				}
				took += time.Since(began)
				outs = append(outs, uv)
			}
		}
		return outs, took / time.Duration(len(outs))
	}
	// cached, uncached, cached again: what one view left in memory or the caches must not show in the other
	a, ta := run(true)
	b, tb := run(false)
	a2, _ := run(true)
	for i := range a {
		if bytes.Equal(a[i], pics[i%len(pics)]) {
			t.Fatalf("picture %d: unchanged", i)
		}
		for k, o := range [][]byte{b[i], a2[i]} {
			if !bytes.Equal(a[i], o) {
				t.Fatalf("picture %d (%+v): run %d differs from the cached run", i%len(pics), tunings[i/len(pics)], k+2)
			}
		}
	}
	t.Logf("%d pictures the same both ways; NV12 %s a picture cached, %s uncached", len(a),
		ta.Round(10*time.Microsecond), tb.Round(10*time.Microsecond))
}

// On a Show 8 with the VPU driver: apunn (as person detection holds it) and swnr (as the stream does)
// on the one shared device, in turn: each run loads its algorithm again, the conv's output stays
// byte-exact, and swnr still smooths. Also the person detector's net size against swnr's part.
func TestDeviceSharedAPUNNAndSWNR(t *testing.T) {
	if !Available() {
		t.Skip("no " + Path)
	}
	a, lm, err := OpenAPUNN()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if a != b {
		t.Fatal("two devices")
	}
	c, err := b.NewChroma(1456, 1088, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("swnr's part of the data area from %#x", c.base)
	conv, in := testConv(false, 40, 40, 16, 96, 3, 1, 0, 7)
	pr, err := conv.Prepare(lm)
	if err != nil {
		t.Fatal(err)
	}
	const inOff, outOff, constOff = ReservedData, 0x80000, 0x100000
	want := conv.Reference(in)
	data := a.Data()
	u, v := c.Planes()
	for round := 0; round < 3; round++ {
		copy(data[inOff:], in)
		clear(data[outOff : outOff+len(want)])
		began := time.Now()
		if err := a.Conv(pr, inOff, uint32(len(in)), outOff, uint32(len(want)), constOff, 2000); err != nil {
			t.Fatal(err)
		}
		convTook := time.Since(began)
		if !bytes.Equal(data[outOff:outOff+len(want)], want) {
			t.Fatalf("round %d: the conv differs after swnr ran", round)
		}
		rng := rand.New(rand.NewSource(int64(round)))
		for i := range u {
			u[i], v[i] = byte(128+rng.Intn(9)-4), byte(128+rng.Intn(9)-4)
		}
		began = time.Now()
		if err := c.Planar(NRParams{K: 600, Strength: 150, BlendW: 16, BlendTh: 10}, 1000); err != nil {
			t.Fatal(err)
		}
		nrTook := time.Since(began)
		dev := 0.0
		for i := range u {
			dev += math.Abs(float64(u[i]) - 128)
		}
		t.Logf("round %d: conv %s, swnr %s (each with its load), U's mean |deviation| %.2f (2.2 before)", round,
			convTook.Round(10*time.Microsecond), nrTook.Round(10*time.Microsecond), dev/float64(len(u)))
	}
}

// On a Show 8 with the VPU driver: where the time of NV12 goes, step by step (one band, on one core),
// on a 1456x1088 picture's chroma in the cores' own memory. Through the data area's uncached view
// (HW_UNCACHED=1, or a kernel without the cached one), reading the planes back is the slow step.
func TestDeviceSWNRCost(t *testing.T) {
	const W, H = 1456, 1088
	d, c := openSWNR(t, W, H)
	if os.Getenv("HW_UNCACHED") != "" {
		c.planes, c.cached, c.buf = d.Data(), false, nil
	}
	t.Logf("the planes through the data area's %s view", map[bool]string{true: "cached", false: "uncached"}[c.cached])
	p := NRParams{K: 1000, Strength: 600, BlendW: 8, BlendTh: 60}
	uv := make([]byte, W*H/2)
	for i := range uv {
		uv[i] = byte(128 + i%7 - 3)
	}
	if err := c.NV12(uv, W, p, 1000); err != nil { // tables, buffers
		t.Fatal(err)
	}
	began := time.Now()
	for i := 0; i < 5; i++ {
		if err := c.NV12(uv, W, p, 1000); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("NV12: %s a picture", time.Since(began)/5)
	n, pl := len(uv), int(c.planeLen())
	in, u, v := make([]byte, n), make([]byte, pl), make([]byte, pl)
	du, dv := c.Planes()
	var took [7]time.Duration
	steps := []func(){
		func() { copy(in, uv) },
		func() { Deinterleave(u, v, in, W/2, H/2, W) },
		func() { copy(du, u); copy(dv, v) },
		func() {
			if err := c.Planar(p, 1000); err != nil {
				t.Fatal(err)
			}
		},
		func() { copy(u, du); copy(v, dv) },
		func() { Interleave(in, u, v, W/2, H/2, W) },
		func() { copy(uv, in) },
	}
	for r := 0; r < 5; r++ {
		for i, f := range steps {
			began := time.Now()
			f()
			took[i] += time.Since(began)
		}
	}
	for i := range took {
		took[i] /= 5
	}
	t.Logf("copy in %s, split %s, to the data area %s, the VPU %s, back %s, join %s, copy out %s", took[0], took[1], took[2], took[3], took[4], took[5], took[6])
}
