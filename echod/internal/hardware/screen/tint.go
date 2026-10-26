//go:build !dot && !panel

package screen

import (
	"errors"
	"image"
	"log/slog"
	"math"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/gpu"
)

// A tint scales every pixel's red, green and blue on its way to the panel: the night's warmer white.
// Where techo5-gpu drives the panel it is the display pipeline's own color correction (the CRTC's
// CTM, the SoC's CCORR block), set once and free for every frame after, video included. On the
// framebuffer it is a table lookup a pixel over a copy of each frame, the canvas itself left as it was
// drawn, so what is drawn once and kept is never tinted twice.

// Tint is the gain on red, green and blue, each 0 to 1 of the value drawn; {1, 1, 1} is none.
type Tint [3]float64

// None is no tint.
var None = Tint{1, 1, 1}

// CanTint is whether SetTint changes what the panel shows.
const CanTint = true

func (t Tint) none() bool { return t == None || t == Tint{} }

// SetTint has the panel show everything with t from the next frame (the next vblank, in the pipeline).
func (d *Device) SetTint(t Tint) {
	if t.none() {
		t = None
	}
	if t == d.tint || t.none() && d.tint.none() {
		return
	}
	d.tint = t
	d.tintTable = nil
	if d.gpu != nil {
		d.tintGPU()
		return
	}
	d.tintTable = tintTable(t)
}

// tintGPU has the display pipeline show d.tint.
func (d *Device) tintGPU() {
	var c gpu.Color
	if !d.tint.none() {
		t := d.tint
		c.Matrix = &[9]float64{t[0], 0, 0, 0, t[1], 0, 0, 0, t[2]}
	}
	// No helper running is the next present's to find (it falls back); only a pipeline that cannot
	// tint is said.
	if _, err := d.gpu.SetColor(c); err != nil && !errors.Is(err, gpu.ErrNoHelper) && !d.tintWarned {
		// The GPU reads the canvas as it is, so there is nowhere on this path to tint in software.
		slog.Warn("screen: the display pipeline cannot tint; the screen is shown untinted", "err", err)
		d.tintWarned = true
	}
}

// tintTable is t as a table a channel; nil for none.
func tintTable(t Tint) *[3][256]uint8 {
	if t.none() {
		return nil
	}
	var lut [3][256]uint8
	for c := range lut {
		g := min(max(t[c], 0), 1)
		for v := range lut[c] {
			lut[c][v] = uint8(math.Round(float64(v) * g))
		}
	}
	return &lut
}

// tinted is the canvas through d.tintTable, into a picture of its own reused frame after frame.
func (d *Device) tinted() *image.RGBA {
	src := d.canvas
	if d.tintImg == nil || d.tintImg.Rect != src.Rect {
		d.tintImg = image.NewRGBA(src.Rect)
	}
	dst, lut := d.tintImg, d.tintTable
	for i := 0; i+3 < len(src.Pix) && i+3 < len(dst.Pix); i += 4 {
		p := src.Pix[i : i+4 : i+4]
		q := dst.Pix[i : i+4 : i+4]
		q[0], q[1], q[2], q[3] = lut[0][p[0]], lut[1][p[1]], lut[2][p[2]], p[3]
	}
	return dst
}
