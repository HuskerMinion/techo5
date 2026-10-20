//go:build !dot

package camstream

// Noise reduction on the Show 8's stream in dim light: the unit's own swnr on its VPU (hardware/vpu's
// Chroma; custom-kernel/athena/research/vpu-vision-p6.md §18), between the ISP's picture and the
// encoder (hardware/camstream's SetDenoise). It smooths the picture's color, which in a dim room is
// most of what makes it look noisy (blotches of red and green over everything gray), keeping color
// edges; the luma is left as the ISP made it.
//
// Only while the frames come with a high gain: the exposure lengthens the frame down to 15 a second
// before it adds gain past 6x (hardware/camera), so a high gain is a dim room. It costs about 20 ms a
// picture (13 on the VPU, the rest the cores moving the plane through the VPU's data area, mapped
// cached), run while the ISP develops the next frame, so the stream keeps its 30 frames a second
// between 4x and 6x, for a frame of delay from the first dim frame until the stream stops. The VPU is
// powered only while it is used (taken at the first dim frame, let go after nrIdle without one, and
// when the stream stops), and shared with person detection (vpu.Acquire). Any failure turns it off
// for the rest of the stream, said once.

import (
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/vpu"
)

// The gains (in 1/64) at which the color is smoothed: from nrOnGain, until it falls under
// nrOffGain.
const (
	nrOnGain  = 4 * 64
	nrOffGain = 3 * 64
	nrMaxGain = 992 // the sensor's ceiling (hardware/camera's aeMaxGain)
	// nrIdle is how long the VPU stays held after the last dim frame.
	nrIdle = 10 * time.Second
	// nrTimeout is how long one of the three jobs of a picture may take before it counts as failed
	// (they take about 4.5 ms each).
	nrTimeout = 250
)

var (
	// nrPictures counts the pictures smoothed, and nrTime the time they took (ns), for tests.
	nrPictures, nrTime atomic.Int64
	// nrAvailable is whether the board has the VPU's driver. Replaced in tests.
	nrAvailable = vpu.Available
	// openNR takes the VPU and readies swnr for w x h pictures. Replaced in tests.
	openNR = openChroma
)

// chromaFilter is swnr readied for the stream's pictures.
type chromaFilter interface {
	nv12(uv []byte, stride int, p vpu.NRParams) error
	close()
}

// streamNR decides frame by frame whether the stream's picture is smoothed, and holds the VPU while
// it is.
type streamNR struct {
	w, h   int
	next   int // the gain of the frame about to be encoded, set before it is
	gain   int // the frame's whose picture is denoised (start)
	on     bool
	failed bool
	f      chromaFilter // nil while the VPU is let go
	used   time.Time
	said   bool
}

// nrParams is the tuning for a gain, from nrLow at nrOnGain to nrHigh at the sensor's ceiling, in a
// straight line between. nrHigh was chosen on the unit's camera in a dark room at the ceiling (§18:
// the color's error against the scene's mean over 24 frames from 10.3 to 3.2 levels); nrLow is
// milder, for a picture with less noise and more color to keep, and not measured.
func nrParams(gain int) vpu.NRParams {
	f := min(max(float64(gain-nrOnGain)/float64(nrMaxGain-nrOnGain), 0), 1)
	at := func(lo, hi int) int { return int(float64(lo) + f*float64(hi-lo) + 0.5) }
	return vpu.NRParams{K: at(nrLow.K, nrHigh.K), Strength: at(nrLow.Strength, nrHigh.Strength),
		BlendW: at(nrLow.BlendW, nrHigh.BlendW), BlendTh: at(nrLow.BlendTh, nrHigh.BlendTh)}
}

var (
	nrLow  = vpu.NRParams{K: 600, Strength: 100, BlendW: 24, BlendTh: 15}
	nrHigh = vpu.NRParams{K: 1000, Strength: 600, BlendW: 8, BlendTh: 60}
)

// start is hardware/camstream's SetDenoise: called as a picture is developed, once the work on the
// one before has ended, it takes the frame's gain and returns the work on the picture, nil when there
// is none (not dim, and the VPU let go).
func (n *streamNR) start() func(uv []byte, stride int) {
	n.gain = n.next
	if !n.dim() && n.f == nil {
		return nil
	}
	return n.denoise
}

// dim is whether the color is smoothed at the frame's gain, with hysteresis.
func (n *streamNR) dim() bool {
	switch {
	case n.gain >= nrOnGain:
		n.on = true
	case n.gain < nrOffGain:
		n.on = false
	}
	return n.on && !n.failed
}

// denoise smooths the color of one picture if its gain calls for it.
func (n *streamNR) denoise(uv []byte, stride int) {
	now := time.Now()
	if !n.dim() {
		if n.f != nil && now.Sub(n.used) >= nrIdle {
			n.release()
		}
		return
	}
	if n.f == nil {
		began := time.Now()
		f, err := openNR(n.w, n.h)
		if err != nil {
			n.fail(err)
			return
		}
		n.f = f
		if !n.said {
			n.said = true
			slog.Info("camera streaming: dim light, the color is smoothed on the VPU", "gain", float64(n.gain)/64,
				"started_in", time.Since(began).Round(time.Millisecond))
		}
	}
	n.used = now
	if err := n.f.nv12(uv, stride, nrParams(n.gain)); err != nil {
		n.fail(err)
		return
	}
	nrPictures.Add(1)
	nrTime.Add(int64(time.Since(now)))
}

func (n *streamNR) fail(err error) {
	n.failed = true
	n.release()
	slog.Warn("camera streaming: the VPU's noise reduction failed; off until the stream starts again", "err", err)
}

func (n *streamNR) release() {
	if n.f != nil {
		n.f.close()
		n.f = nil
	}
}

type vpuChroma struct {
	d *vpu.Device
	c *vpu.Chroma
}

func openChroma(w, h int) (chromaFilter, error) {
	d, err := vpu.Acquire()
	if err != nil {
		return nil, err
	}
	c, err := d.NewChroma(w, h, nil)
	if err != nil {
		d.Close()
		return nil, err
	}
	return &vpuChroma{d, c}, nil
}

func (v *vpuChroma) nv12(uv []byte, stride int, p vpu.NRParams) error {
	return v.c.NV12(uv, stride, p, nrTimeout)
}

func (v *vpuChroma) close() { v.d.Close() }
