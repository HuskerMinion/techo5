//go:build linux && !dot

package person

import (
	"errors"
	"math"
	"os"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/vpu"
	"github.com/HuskerMinion/techo5/echod/internal/lib/tflite"
)

func loadQuant(t testing.TB) (*detector, *quantModel) {
	t.Helper()
	m, err := tflite.Parse(model)
	if err != nil {
		t.Fatal(err)
	}
	in, err := tflite.New(m)
	if err != nil {
		t.Fatal(err)
	}
	q, err := quantize(m, in)
	if err != nil {
		t.Fatal(err)
	}
	return &detector{in: in}, q
}

// testImages are 96x96 gray pictures, back to back, from PERSON_IMAGES; or a few made up.
func testImages(t testing.TB) [][]uint8 {
	var out [][]uint8
	if p := os.Getenv("PERSON_IMAGES"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for len(b) >= side*side {
			out, b = append(out, b[:side*side]), b[side*side:]
		}
		return out
	}
	for k := 0; k < 6; k++ {
		g := make([]uint8, side*side)
		for i := range g {
			x, y := i%side, i/side
			g[i] = uint8(128 + 100*math.Sin(float64(x*(k+1))/9)*math.Cos(float64(y*(k+2))/13))
		}
		out = append(out, g)
	}
	return out
}

// The model as the VPU takes it: 27 convolutions (the first, a depthwise from one channel, made a
// conv), then the 1x1 to two classes after the pool.
func TestQuantize(t *testing.T) {
	_, q := loadQuant(t)
	if len(q.convs) != 27 {
		t.Fatalf("%d convolutions", len(q.convs))
	}
	if c := q.convs[0]; c.Depthwise || c.InC != 1 || c.OutC != 8 || c.Stride != 2 || c.InW != side {
		t.Fatalf("the first conv: %+v", *c)
	}
	for i, c := range q.convs[1:] {
		p := q.convs[i]
		if c.InW != p.OutW() || c.InC != p.OutC || c.InScale != p.OutScale || c.InZero != p.OutZero {
			t.Fatalf("conv %d does not take what conv %d gives", i+1, i)
		}
		if c.Depthwise && c.InC != c.OutC {
			t.Fatalf("conv %d: a depthwise multiplier", i+1)
		}
	}
	if q.trunkOut() != 3*3*256 || q.final.InC != 256 || q.final.OutC != 2 {
		t.Fatalf("the tail: %d, %+v", q.trunkOut(), *q.final)
	}
}

// How far uint8 per tensor moves the score from the float model's, and whether it decides alike.
func TestPerTensorScores(t *testing.T) {
	d, q := loadQuant(t)
	var worst, sum float64
	flips := 0
	imgs := testImages(t)
	for i, g := range imgs {
		f, err := d.score(g)
		if err != nil {
			t.Fatal(err)
		}
		u := q.tail(q.cpu(g))
		e := math.Abs(float64(f - u))
		worst, sum = max(worst, e), sum+e
		if (f >= threshold) != (u >= threshold) {
			flips++
		}
		t.Logf("picture %2d: float %.3f, uint8 per tensor %.3f", i, f, u)
	}
	t.Logf("%d pictures: mean |difference| %.3f, worst %.3f; %d decided otherwise at %.1f", len(imgs), sum/float64(len(imgs)), worst, flips, threshold)
	if sum/float64(len(imgs)) > 0.08 || worst > 0.2 {
		t.Fatal("uint8 per tensor is not the model any more")
	}
}

// fakeTrunk computes what the VPU would, on the CPU, or fails.
type fakeTrunk struct {
	q            *quantModel
	fail         bool
	runs, closes int
}

func (f *fakeTrunk) run(gray, out []uint8) error {
	f.runs++
	if f.fail {
		return errors.New("the VPU took an exception")
	}
	copy(out, f.q.cpu(gray))
	return nil
}

func (f *fakeTrunk) close() { f.closes++ }

// withVPU makes the board one with a VPU whose trunk is fake, for the length of a test.
func withVPU(t *testing.T, open func(*quantModel) (trunk, error)) {
	avail, op := vpuAvailable, openTrunk
	vpuAvailable, openTrunk = func() bool { return true }, open
	vpuFailed.Store(false)
	t.Cleanup(func() {
		vpuAvailable, openTrunk = avail, op
		vpuFailed.Store(false)
	})
}

func detectorWithVPU(t *testing.T) *detector {
	t.Helper()
	d, err := load()
	if err != nil {
		t.Fatal(err)
	}
	if d.vpu == nil {
		t.Fatal("no VPU path on a board with a VPU")
	}
	return d
}

func TestVPUScores(t *testing.T) {
	_, q := loadQuant(t)
	ft := &fakeTrunk{q: q}
	withVPU(t, func(*quantModel) (trunk, error) { return ft, nil })
	d := detectorWithVPU(t)
	g := testImages(t)[0]
	p, err := d.score(g)
	if err != nil {
		t.Fatal(err)
	}
	if want := q.tail(q.cpu(g)); p != want || ft.runs != 1 {
		t.Fatalf("%v, want %v (%d runs)", p, want, ft.runs)
	}
	d.close()
	if ft.closes != 1 {
		t.Fatal("the VPU not let go")
	}
}

// A VPU that fails once is not used again: the score comes from the cores, then and after.
func TestVPUFailsOverToCPU(t *testing.T) {
	_, q := loadQuant(t)
	ft := &fakeTrunk{q: q, fail: true}
	opens := 0
	withVPU(t, func(*quantModel) (trunk, error) { opens++; return ft, nil })
	d := detectorWithVPU(t)
	cpu := &detector{in: d.in}
	g := testImages(t)[1]
	want, _ := cpu.score(g)
	for range 3 {
		p, err := d.score(g)
		if err != nil || p != want {
			t.Fatalf("%v (%v), want the cores' %v", p, err, want)
		}
	}
	if opens != 1 || ft.runs != 1 || ft.closes != 1 || !vpuFailed.Load() {
		t.Fatalf("%d opens, %d runs, %d closes", opens, ft.runs, ft.closes)
	}
	if e, _ := load(); e.vpu != nil {
		t.Fatal("a failed VPU taken again")
	}

	// One that cannot even be opened, the same.
	vpuFailed.Store(false)
	openTrunk = func(*quantModel) (trunk, error) { return nil, errors.New("busy") }
	d = detectorWithVPU(t)
	if p, err := d.score(g); err != nil || p != want || !vpuFailed.Load() {
		t.Fatalf("%v (%v), want the cores' %v", p, err, want)
	}
}

// The VPU is powered from the first look to vpuIdle after the last one.
func TestVPUIdle(t *testing.T) {
	_, q := loadQuant(t)
	opens, closes := 0, 0
	withVPU(t, func(*quantModel) (trunk, error) { opens++; return &closer{fakeTrunk{q: q}, &closes}, nil })
	d := detectorWithVPU(t)
	a := d.vpu
	at := time.Unix(1000, 0)
	g := testImages(t)[2]
	if _, ok := a.score(g, at); !ok || opens != 1 {
		t.Fatal("not opened at the first look")
	}
	a.score(g, at.Add(time.Second))
	a.idle(at.Add(vpuIdle))
	if closes != 0 {
		t.Fatal("let go while in use")
	}
	a.idle(at.Add(time.Second + vpuIdle))
	if closes != 1 {
		t.Fatal("not let go when idle")
	}
	a.idle(at.Add(time.Hour))
	if _, ok := a.score(g, at.Add(time.Hour)); !ok || opens != 2 || closes != 1 {
		t.Fatalf("%d opens, %d closes", opens, closes)
	}
	d.close()
	d.close()
	if closes != 2 {
		t.Fatalf("%d closes", closes)
	}
}

type closer struct {
	fakeTrunk
	n *int
}

func (c *closer) close() { *c.n++ }

func BenchmarkTail(b *testing.B) {
	_, q := loadQuant(b)
	last := q.cpu(testImages(b)[0])
	for b.Loop() {
		q.tail(last)
	}
}

// Equalization leaves every path through a channel as it was (a weight of one layer times one of the
// next) and gives the channel the same range in both layers.
func TestEqualize(t *testing.T) {
	m, err := tflite.Parse(model)
	if err != nil {
		t.Fatal(err)
	}
	in, err := tflite.New(m)
	if err != nil {
		t.Fatal(err)
	}
	g := m.Subgraphs[0]
	var convs []*vpu.Conv
	var ws, bs [][]float64
	act := func(i int) (float64, uint8, error) {
		d := g.Tensors[i]
		return float64(d.Scale[0]), uint8(d.ZeroPoint[0] + 128), nil
	}
	for _, o := range g.Ops[:3] {
		c, w, b, err := convOf(g, in, o, act)
		if err != nil {
			t.Fatal(err)
		}
		convs, ws, bs = append(convs, c), append(ws, w), append(bs, b)
	}
	p, n := convs[1], convs[2] // the depthwise, then the 1x1
	path := func(ch int) []float64 {
		var v []float64
		eachOut(p, ch, func(i int) {
			eachIn(n, ch, func(k int) { v = append(v, ws[1][i]*ws[2][k]) })
		})
		return v
	}
	before := path(5)
	equalize(convs[1:], ws[1:], bs[1:], 1, 1)
	for k, v := range path(5) {
		if math.Abs(v-before[k]) > 1e-9*math.Abs(before[k])+1e-12 {
			t.Fatalf("path %d: %v, was %v", k, v, before[k])
		}
	}
	for ch := 0; ch < p.OutC; ch++ {
		r1, r2 := 0.0, 0.0
		eachOut(p, ch, func(k int) { r1 = max(r1, math.Abs(ws[1][k])) })
		eachIn(n, ch, func(k int) { r2 = max(r2, math.Abs(ws[2][k])) })
		if math.Abs(r1-r2) > 1e-9*r1 {
			t.Fatalf("channel %d: ranges %v and %v", ch, r1, r2)
		}
	}
}
