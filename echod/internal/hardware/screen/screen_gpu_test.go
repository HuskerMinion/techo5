//go:build linux && !dot && !panel

package screen

import (
	"image"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/gpu"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/gpu/gputest"
)

func TestMain(m *testing.M) {
	gputest.Main()
	os.Exit(m.Run())
}

// gpuDevice is a 4x8 portrait panel in memory, its framebuffer laid out B, G, R, A as athena's is,
// with the fake helper drawing it where cfg lets it.
func gpuDevice(t *testing.T, cfg gputest.Config) (*Device, *gputest.Log) {
	t.Helper()
	oldFirst, oldRetry := gpuRetryFirst, gpu.RetryAfter
	gpuRetryFirst, gpu.RetryAfter = 30*time.Millisecond, 0
	t.Cleanup(func() { gpuRetryFirst, gpu.RetryAfter = oldFirst, oldRetry; useLift(nil) })
	log := gputest.Use(t, cfg)
	d := &Device{panelW: 4, panelH: 8, line: 16, pages: 1, shift: [4]uint{16, 8, 0, 24}}
	d.pageBytes = d.line * d.panelH
	d.mem = make([]byte, d.pageBytes)
	if rotated {
		d.canvas = image.NewRGBA(image.Rect(0, 0, d.panelH, d.panelW))
	} else {
		d.canvas = image.NewRGBA(image.Rect(0, 0, d.panelW, d.panelH))
	}
	d.useGPU()
	t.Cleanup(func() { d.Close() })
	return d, log
}

func helperPid(t *testing.T, line string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.Fields(line)[0])
	if err != nil {
		t.Fatalf("log line %q", line)
	}
	return n
}

// The helper dying mid-run: the screen stops it, which gives the panel back to the kernel, draws the
// same canvas on the framebuffer from that frame on, keeps its frame layout, and takes the GPU back
// after the wait.
func TestGPUFallBackAndBack(t *testing.T) {
	d, log := gpuDevice(t, gputest.Config{Panel: "4x8", DieAt: 2})
	if d.gpu == nil || !DrawnByGPU() {
		t.Fatal("not drawn by the GPU")
	}
	fmtBefore := d.PixFmt()
	canvas := d.Canvas()
	for i := range canvas.Pix {
		canvas.Pix[i] = 0x80
	}
	if err := d.Present(); err != nil {
		t.Fatal(err)
	}
	first := helperPid(t, log.Ops("hello")[0])
	if err := d.Present(); err != nil { // the helper dies here; the cores draw this frame
		t.Fatal(err)
	}
	if err := d.Settle(); err != nil {
		t.Fatal(err)
	}
	if d.gpu != nil || DrawnByGPU() {
		t.Fatal("still on the GPU after it died")
	}
	if !gputest.Gone(first) {
		t.Fatal("the dead helper's process is still there")
	}
	if d.mem[0] != 0x80 {
		t.Fatalf("the framebuffer was not drawn: %x", d.mem[:8])
	}
	if d.PixFmt() != fmtBefore || d.PixFmt() != "bgra" {
		t.Errorf("frame layout %q after the fallback, %q before, want bgra both", d.PixFmt(), fmtBefore)
	}
	if d.Canvas() != canvas {
		t.Error("the canvas changed under the drawing")
	}
	// Before the wait is over the cores draw; after it, the GPU again, with a new helper.
	if err := d.Present(); err != nil || d.gpu != nil {
		t.Fatalf("within the wait: err %v, on the GPU %v", err, d.gpu != nil)
	}
	time.Sleep(2 * gpuRetryFirst)
	canvas.Pix[0] = 0x42
	if err := d.Present(); err != nil {
		t.Fatal(err)
	}
	if d.gpu == nil || !DrawnByGPU() {
		t.Fatal("the GPU was not taken back after the wait")
	}
	p := log.Ops("present")
	last := p[len(p)-1]
	if helperPid(t, last) == first || !strings.Contains(last, "first=66,") {
		t.Errorf("presents %q: want the canvas drawn by a new helper", p)
	}
	// A frame goes in the framebuffer's order, whichever draws it.
	if err := d.PresentFrame(make([]byte, 4*8*4), image.Rectangle{}); err != nil {
		t.Fatal(err)
	}
	if f := log.Ops("frame"); len(f) != 1 || !strings.Contains(f[0], "layout=1") {
		t.Errorf("frames %q: want the framebuffer's layout", f)
	}
}

// A failure soon after coming back doubles the wait.
func TestGPURetryBacksOff(t *testing.T) {
	d, _ := gpuDevice(t, gputest.Config{Panel: "4x8"})
	d.leaveGPU(errTest)
	if d.gpuPath.wait != gpuRetryFirst {
		t.Fatalf("first wait %v", d.gpuPath.wait)
	}
	d.gpuPath.retryAt = time.Now()
	d.retryGPU()
	if d.gpu == nil {
		t.Fatal("not back")
	}
	d.leaveGPU(errTest)
	if d.gpuPath.wait != 2*gpuRetryFirst {
		t.Fatalf("wait after a short run %v, want %v", d.gpuPath.wait, 2*gpuRetryFirst)
	}
	d.gpuPath.retryAt = time.Now()
	d.retryGPU()
	d.gpuPath.since = time.Now().Add(-gpuSteady)
	d.leaveGPU(errTest)
	if d.gpuPath.wait != gpuRetryFirst {
		t.Fatalf("wait after a steady run %v, want %v", d.gpuPath.wait, gpuRetryFirst)
	}
}

var errTest = os.ErrDeadlineExceeded

// A panel mode the GPU cannot match: its helper is stopped at once and not tried again.
func TestGPUPanelMismatch(t *testing.T) {
	d, log := gpuDevice(t, gputest.Config{Panel: "5x5"})
	if d.gpu != nil || DrawnByGPU() {
		t.Fatal("on the GPU with a panel mode that differs")
	}
	if h := log.Ops("hello"); len(h) != 1 || !gputest.Gone(helperPid(t, h[0])) {
		t.Fatalf("hellos %q: want the one helper stopped", h)
	}
	if !d.gpuPath.retryAt.IsZero() {
		t.Error("a retry is planned for a panel mode that will not change")
	}
	if err := d.Present(); err != nil {
		t.Fatal(err)
	}
	if len(log.Ops("hello")) != 1 {
		t.Error("the helper was started again")
	}
}

// A skin's row filter reaches the panel through the GPU too: the helper is given the filtered rows,
// and the canvas keeps what was drawn.
func TestGPUFilter(t *testing.T) {
	d, log := gpuDevice(t, gputest.Config{Panel: "4x8"})
	canvas := d.Canvas()
	canvas.Pix[0] = 10
	d.SetFilter("red", func(row []byte, y int) {
		for i := 0; i < len(row); i += 4 {
			row[i] = 123
		}
	})
	if err := d.Present(); err != nil {
		t.Fatal(err)
	}
	d.SetFilter("", nil)
	if err := d.Present(); err != nil {
		t.Fatal(err)
	}
	p := log.Ops("present")
	if len(p) != 2 || !strings.Contains(p[0], "first=123,") || !strings.Contains(p[1], "first=10,") {
		t.Errorf("presents %q: want the filtered rows, then the canvas", p)
	}
	if canvas.Pix[0] != 10 {
		t.Errorf("the canvas was changed by the filter: %d", canvas.Pix[0])
	}
}

// The CRTC keeps a tint after the helper that set it is gone: falling back puts the panel's color
// back to the identity (the helper's --reset-color) before the cores draw it with the tint, so it is
// not shown twice.
func TestGPUFallbackResetsTheTint(t *testing.T) {
	d, log := gpuDevice(t, gputest.Config{Panel: "4x8"})
	warm := Tint{1, 0.8, 0.6}
	d.SetTint(warm)
	if c := log.Ops("color"); len(c) == 0 || d.tintTable != nil {
		t.Fatalf("colors %q, tint table %v: want the tint in the pipeline", c, d.tintTable != nil)
	}
	d.leaveGPU(errTest)
	if r := log.Ops("reset"); len(r) != 1 {
		t.Errorf("resets %q: the panel's color was not put back", r)
	}
	if d.tintTable == nil {
		t.Error("the cores do not tint after the fallback")
	}
}
