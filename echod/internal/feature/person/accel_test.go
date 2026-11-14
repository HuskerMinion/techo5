//go:build !dot

package person

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/tflite"
)

// cpuOnly is the model on the cores, an accelerator let go if the board has one.
func cpuOnly(d *detector) *detector {
	d.close()
	d.acc = nil
	return d
}

// fakeAccel scores what it is told to, or fails.
type fakeAccel struct {
	p              float32
	fail           bool
	scores, closes int
	idled          time.Time
}

func (a *fakeAccel) score([]uint8, time.Time) (float32, bool) {
	a.scores++
	return a.p, !a.fail
}
func (a *fakeAccel) idle(now time.Time) { a.idled = now }
func (a *fakeAccel) close()             { a.closes++ }

func withAccelerators(t *testing.T, opens ...func(*tflite.Model, *tflite.Interpreter) accelerator) {
	t.Helper()
	saved := accelerators
	accelerators = opens
	t.Cleanup(func() { accelerators = saved })
}

// The first accelerator that opens for the model is the one used, in the order registered; none is
// the cores.
func TestTheFirstAcceleratorThatOpensIsUsed(t *testing.T) {
	second := &fakeAccel{p: 0.75}
	asked := 0
	withAccelerators(t,
		func(*tflite.Model, *tflite.Interpreter) accelerator { asked++; return nil },
		func(*tflite.Model, *tflite.Interpreter) accelerator { asked++; return second },
		func(*tflite.Model, *tflite.Interpreter) accelerator {
			asked++
			t.Error("asked past the one that opened")
			return nil
		})
	d, err := load()
	if err != nil {
		t.Fatal(err)
	}
	if d.acc != second || asked != 2 {
		t.Fatalf("the detector has %v after %d asked, want the second", d.acc, asked)
	}
	if p, err := d.score(make([]uint8, side*side)); err != nil || p != 0.75 {
		t.Fatalf("%v (%v), want the accelerator's 0.75", p, err)
	}
	at := time.Unix(1000, 0)
	d.idle(at)
	d.close()
	if second.idled != at || second.closes != 1 {
		t.Fatalf("idle %v, %d closes", second.idled, second.closes)
	}

	withAccelerators(t)
	if d, err := load(); err != nil || d.acc != nil {
		t.Fatalf("with none registered: %v (%v)", d.acc, err)
	}
}

// An accelerator that fails once is let go, and the cores score that picture and every one after.
func TestAFailingAcceleratorLeavesItToTheCores(t *testing.T) {
	bad := &fakeAccel{p: 0.5, fail: true}
	withAccelerators(t, func(*tflite.Model, *tflite.Interpreter) accelerator { return bad })
	d, err := load()
	if err != nil {
		t.Fatal(err)
	}
	cpu := &detector{in: d.in}
	gray := make([]uint8, side*side)
	for i := range gray {
		gray[i] = uint8(i % 251)
	}
	want, _ := cpu.score(gray)
	for range 2 {
		if p, err := d.score(gray); err != nil || p != want {
			t.Fatalf("%v (%v), want the cores' %v", p, err, want)
		}
	}
	if bad.scores != 1 || d.acc != nil {
		t.Fatalf("the failed accelerator asked %d times, still held: %v", bad.scores, d.acc != nil)
	}
}
