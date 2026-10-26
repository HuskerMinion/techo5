//go:build !dot && !panel

package screen

import (
	"errors"
	"image"
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/gpu"
)

// The GPU path (the mainline 2nd gen Show 8 and Show 5, with techo5-gpu in the root filesystem): the
// canvas lives in memory the helper shares, and Present has the GPU draw it turned onto the panel and
// show it with a page flip at vblank, where the framebuffer path turns it on the cores and writes it
// row by row. A video frame goes the same way, the controls blended over it on the GPU.
//
// Should the helper fail, the screen goes back to the framebuffer it still holds: the helper is
// stopped, which gives the panel back to the kernel's console framebuffer, and every page is written
// afresh. The canvas stays where it is, so whatever draws on it goes on as before, and after
// gpuRetryFirst the GPU is tried again with it; each failure after a short run doubles the wait, up
// to gpuRetryMax.

var (
	// gpuRetryFirst is how long the cores draw after the GPU failed before it is tried again: the
	// helper's own wait (gpu.RetryAfter). Variables for tests.
	gpuRetryFirst = time.Minute
	gpuRetryMax   = 30 * time.Minute
	// gpuSteady is how long the GPU has to draw for its next failure to be waited out from
	// gpuRetryFirst again.
	gpuSteady = 10 * time.Minute
)

// gpuState is the GPU path's: the display (kept while the cores draw, for the retry), whether it is
// drawing, and when to try it again.
type gpuState struct {
	disp    *gpu.Display
	since   time.Time     // when it last took the panel
	retryAt time.Time     // when to try it again, while the cores draw; zero: never
	wait    time.Duration // the wait after the last failure
}

// useGPU moves the canvas into the helper's shared memory, and has the helper drive the panel when it
// can. Where the root filesystem carries a helper the canvas is the shared one even when the helper
// does not answer now, so a later retry can take the panel with it.
func (d *Device) useGPU() {
	if !gpu.Installed() {
		return
	}
	w, h := d.canvas.Rect.Dx(), d.canvas.Rect.Dy()
	disp, canvas, err := gpu.NewDisplay(w, h, rotated)
	if err != nil {
		slog.Warn("screen: no canvas for the GPU; drawing on the cores", "err", err)
		return
	}
	copy(canvas.Pix, d.canvas.Pix)
	d.canvas = canvas
	d.gpuPath.disp = disp
	if err := d.enterGPU(); err != nil {
		if errors.Is(err, errPanelMode) {
			// Nothing a retry would change: the helper is stopped and the panel is the cores' for good.
			slog.Warn("screen: drawing on the cores", "err", err)
			return
		}
		slog.Warn("screen: the GPU cannot drive the panel now; drawing on the cores", "err", err)
		d.gpuPath.wait = gpuRetryFirst
		d.gpuPath.retryAt = time.Now().Add(gpuRetryFirst)
	}
}

var errPanelMode = errors.New("screen: the GPU's panel mode differs from the framebuffer's")

// enterGPU has the helper take the panel with the canvas. On failure it is stopped, so the panel stays
// the framebuffer's.
func (d *Device) enterGPU() error {
	disp := d.gpuPath.disp
	if err := disp.Open(); err != nil {
		disp.Release(err)
		return err
	}
	if p := disp.Panel(); p.X != d.panelW || p.Y != d.panelH {
		slog.Warn("screen: the GPU's panel mode differs from the framebuffer's",
			"gpu", p, "fb", image.Pt(d.panelW, d.panelH))
		disp.Release(errPanelMode)
		return errPanelMode
	}
	d.gpu = disp
	d.gpuPath.since = time.Now()
	d.gpuPath.retryAt = time.Time{}
	d.tintTable = nil // the pipeline tints from now, with the tint as it is now
	d.tintGPU()
	useLift(disp)
	slog.Info("screen: drawn by the GPU", "gpu", gpu.Name(), "canvas", d.canvas.Rect.Size(), "panel", disp.Panel())
	return nil
}

// retryGPU tries the GPU again once its wait is over.
func (d *Device) retryGPU() {
	g := &d.gpuPath
	if d.gpu != nil || g.disp == nil || g.retryAt.IsZero() || time.Now().Before(g.retryAt) {
		return
	}
	if err := d.enterGPU(); err != nil {
		if errors.Is(err, errPanelMode) {
			g.retryAt = time.Time{}
			return
		}
		g.wait = min(2*g.wait, gpuRetryMax)
		g.retryAt = time.Now().Add(g.wait)
		slog.Warn("screen: the GPU is still failing; drawing on the cores", "err", err, "next_try", g.wait)
		return
	}
	// The pages were written by the cores meanwhile; the next fallback writes them afresh again.
	for i := range d.stale {
		d.stale[i] = true
	}
}

// gpuSource is what the GPU path shows: the canvas, or, with a filter on, the canvas through it into
// the display's staging picture, so the helper reads the filtered rows where they were written.
func (d *Device) gpuSource() *image.RGBA {
	if d.filter.f == nil {
		return d.canvas
	}
	// Where the staging picture cannot be had, the filter's own picture is copied into it by the display.
	if st, err := d.gpu.Staging(); err == nil && !samePix(d.filter.filtered, st) {
		d.filter.filtered, d.filter.prev = st, nil
	}
	return d.filter.over(d.canvas)
}

// samePix is whether a and b are pictures over the same memory.
func samePix(a, b *image.RGBA) bool {
	return a != nil && b != nil && len(a.Pix) > 0 && len(b.Pix) > 0 && &a.Pix[0] == &b.Pix[0]
}

// presentGPU is Present on the GPU; false when there is no GPU path, or it just failed and the
// framebuffer takes over from this frame.
func (d *Device) presentGPU() bool {
	d.retryGPU()
	if d.gpu == nil {
		return false
	}
	if lit.lost() {
		d.leaveGPU(errors.New("screen: the lift found the GPU helper gone"))
		return false
	}
	src := d.gpuSource()
	err := lit.present(true, func(measure bool) (int, error) {
		if measure {
			return d.gpu.PresentMeasured(src)
		}
		return -1, d.gpu.Present(src)
	})
	if err != nil {
		d.leaveGPU(err)
		return false
	}
	return true
}

// presentFrameGPU is PresentFrame on the GPU, frame being in the panel's orientation and laid out as
// PixFmt says (the framebuffer's order, which the GPU takes as it is).
func (d *Device) presentFrameGPU(frame []byte, over image.Rectangle) bool {
	d.retryGPU()
	if d.gpu == nil {
		return false
	}
	layout := gpu.FrameRGBA
	if pixFmt(d.shift) == "bgra" {
		layout = gpu.FrameBGRA
	}
	if err := lit.present(false, func(bool) (int, error) {
		return -1, d.gpu.PresentFrame(frame, d.panelW, d.panelH, layout, over)
	}); err != nil {
		d.leaveGPU(err)
		return false
	}
	return true
}

// leaveGPU goes back to the framebuffer: the helper is stopped, which gives the panel back to the
// kernel, everything on the framebuffer is written afresh, and the GPU is tried again after a wait.
func (d *Device) leaveGPU(err error) {
	g := &d.gpuPath
	slog.Warn("screen: the GPU failed; drawing on the cores", "err", err)
	g.disp.Release(err)
	// The CRTC keeps the tint the helper set: put back to none, the cores tint from now (tintTable).
	if err := gpu.ResetColor(); err != nil {
		slog.Warn("screen: the panel's color could not be put back; a tint may show twice", "err", err)
	}
	d.gpu = nil
	useLift(nil)
	d.tintTable = tintTable(d.tint) // a tint the pipeline was showing is drawn on the cores now
	if st, err := g.disp.Staging(); err == nil && samePix(d.filter.filtered, st) {
		// The filter's rows go to a picture of the screen's own again.
		d.filter.filtered, d.filter.prev = nil, nil
	}
	if len(d.stale) < d.pages {
		d.stale = make([]bool, max(d.pages, 1))
	}
	for i := range d.stale {
		d.stale[i] = true
	}
	if g.wait == 0 || time.Since(g.since) >= gpuSteady {
		g.wait = gpuRetryFirst
	} else {
		g.wait = min(2*g.wait, gpuRetryMax)
	}
	g.retryAt = time.Now().Add(g.wait)
}
