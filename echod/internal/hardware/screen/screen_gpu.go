//go:build !dot && !panel

package screen

import (
	"image"
	"log/slog"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/gpu"
)

// The GPU path (the mainline 2nd gen Show 8 and Show 5, with techo5-gpu in the root filesystem): the
// canvas lives in memory the helper shares, and Present has the GPU draw it turned onto the panel and
// show it with a page flip at vblank, where the framebuffer path turns it on the cores and writes it
// row by row. A video frame goes the same way, the controls blended over it on the GPU. Should the
// helper fail, the device carries on through the framebuffer it still holds: the kernel puts its
// console framebuffer back on the panel when the helper lets go of the display.

// useGPU moves the canvas into the helper's shared memory, when the helper can drive this panel.
func (d *Device) useGPU() {
	if !gpu.Available() {
		return
	}
	w, h := d.canvas.Rect.Dx(), d.canvas.Rect.Dy()
	disp, canvas, err := gpu.OpenDisplay(w, h, rotated)
	if err != nil {
		slog.Warn("screen: the GPU cannot drive the panel; drawing on the cores", "err", err)
		return
	}
	if p := disp.Panel(); p.X != d.panelW || p.Y != d.panelH {
		slog.Warn("screen: the GPU's panel mode differs from the framebuffer's; drawing on the cores",
			"gpu", p, "fb", image.Pt(d.panelW, d.panelH))
		return
	}
	copy(canvas.Pix, d.canvas.Pix)
	d.canvas = canvas
	d.gpu = disp
	useLift(disp)
	slog.Info("screen: drawn by the GPU", "gpu", gpu.Name(), "canvas", d.canvas.Rect.Size(), "panel", disp.Panel())
}

// presentGPU is Present on the GPU; false when there is no GPU path, or it just failed and the
// framebuffer takes over from this frame.
func (d *Device) presentGPU() bool {
	if d.gpu == nil {
		return false
	}
	err := lit.present(true, func(measure bool) (int, error) {
		if measure {
			return d.gpu.PresentMeasured()
		}
		return -1, d.gpu.Present()
	})
	if err != nil {
		d.leaveGPU(err)
		return false
	}
	return true
}

// presentFrameGPU is PresentFrame on the GPU, frame being RGBA in the panel's orientation.
func (d *Device) presentFrameGPU(frame []byte, over image.Rectangle) bool {
	if d.gpu == nil {
		return false
	}
	if err := lit.present(false, func(bool) (int, error) {
		return -1, d.gpu.PresentFrame(frame, d.panelW, d.panelH, over)
	}); err != nil {
		d.leaveGPU(err)
		return false
	}
	return true
}

// leaveGPU goes back to the framebuffer for good: everything on it is written afresh.
func (d *Device) leaveGPU(err error) {
	slog.Warn("screen: the GPU failed; drawing on the cores from now", "err", err)
	d.gpu = nil
	useLift(nil)
	d.tintTable = tintTable(d.tint) // a tint the pipeline was showing is drawn on the cores now
	for i := range d.stale {
		d.stale[i] = true
	}
}
