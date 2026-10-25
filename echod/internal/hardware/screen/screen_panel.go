//go:build panel

// Package screen owns the panel of a device with a screen. On a TECHO5 Panel that is the app's surface:
// the canvas is the size the app asked for (lib/panelscreen), landscape and at most a Show 8's width
// so the Show's layouts fit it as drawn, and Present sends the rectangle that changed since the last
// frame rather than the whole of it. The app scales the canvas up to the tablet's own pixels.
package screen

import (
	"bytes"
	"fmt"
	"image"
	"sync"

	"github.com/HuskerMinion/techo5/echod/internal/lib/panelscreen"
)

// BacklightMax is the top of the brightness the app is asked for.
const BacklightMax = 255

// Device is the app's surface, seen as a framebuffer.
type Device struct {
	// sent is the connection the last frame went down: a new one is an app whose surface starts
	// blank, so it gets the whole canvas.
	sent    *panelscreen.Conn
	density int
	canvas  *image.RGBA

	// prev is what the app was last sent, so a frame sends only what differs: a clock that ticks
	// changes a few rows, and the whole canvas at Show 8 size is 4 MB a frame.
	prev []byte

	// filter is laid over every row on its way to the app (SetFilter, filter.go), as on an Echo's
	// panel: a skin's one color reaches the app's surface the same way.
	filter rowFilter
}

// Open connects to the app and makes a canvas the size it asked for.
func Open() (*Device, error) {
	c, err := panelscreen.Get()
	if err != nil {
		return nil, fmt.Errorf("screen: %w", err)
	}
	return &Device{density: c.Density, canvas: image.NewRGBA(image.Rect(0, 0, c.W, c.H))}, nil
}

// Size is the canvas the app asked for.
func (d *Device) Size() (w, h int) { return d.canvas.Rect.Dx(), d.canvas.Rect.Dy() }

// Pages is one: the app shows a frame once it has all of it, so nothing tears.
func (d *Device) Pages() int { return 1 }

// Canvas is what to draw on. Present shows it.
func (d *Device) Canvas() *image.RGBA { return d.canvas }

func (d *Device) String() string {
	return fmt.Sprintf("panel app surface, %dx%d canvas, %d dpi", d.canvas.Rect.Dx(), d.canvas.Rect.Dy(), d.density)
}

// SetFilter lays f over every row of the canvas on its way to the app, as on an Echo's panel
// (screen.go); nil takes it off. The canvas itself is not touched.
func (d *Device) SetFilter(name string, f func(row []byte, y int)) { d.filter.set(name, f) }

// source is what Present sends: the canvas, or the canvas through the filter.
func (d *Device) source() *image.RGBA { return d.filter.over(d.canvas) }

// Settle is for the same calls as on an Echo's panel (screen.go): Present here is done when it returns.
func (d *Device) Settle() error { return nil }

// Present sends the smallest rectangle holding every pixel that changed, after the filter.
func (d *Device) Present() error {
	img := d.source()
	w, h, stride := img.Rect.Dx(), img.Rect.Dy(), img.Stride
	c, err := panelscreen.Get()
	if err != nil {
		return fmt.Errorf("screen: %w", err)
	}
	if c != d.sent {
		d.prev, d.sent = nil, c
	}
	if d.prev == nil {
		if err := d.send(img, 0, 0, w, h); err != nil {
			return err
		}
		d.prev = append(d.prev[:0], img.Pix...)
		return nil
	}

	top, bottom, left, right := -1, -1, w, -1
	for y := 0; y < h; y++ {
		row, was := img.Pix[y*stride:y*stride+w*4], d.prev[y*stride:y*stride+w*4]
		if bytes.Equal(row, was) {
			continue
		}
		if top < 0 {
			top = y
		}
		bottom = y
		// Columns only for rows that changed, from each end until they differ.
		l := 0
		for l < w && bytes.Equal(row[l*4:l*4+4], was[l*4:l*4+4]) {
			l++
		}
		r := w - 1
		for r > l && bytes.Equal(row[r*4:r*4+4], was[r*4:r*4+4]) {
			r--
		}
		left, right = min(left, l), max(right, r)
	}
	if top < 0 {
		return nil
	}
	if err := d.send(img, left, top, right-left+1, bottom-top+1); err != nil {
		return err
	}
	for y := top; y <= bottom; y++ {
		at := y*stride + left*4
		copy(d.prev[at:at+(right-left+1)*4], img.Pix[at:at+(right-left+1)*4])
	}
	return nil
}

// send sends the rectangle x, y, w, h of img, the canvas as it goes out.
func (d *Device) send(img *image.RGBA, x, y, w, h int) error {
	if err := d.sent.Frame(img.Pix, img.Stride, x, y, w, h); err != nil {
		panelscreen.Drop(d.sent)
		d.prev, d.sent = nil, nil
		return fmt.Errorf("screen: %w", err)
	}
	return nil
}

// FrameSize is the canvas: a video frame on a panel would be made in its shape, unturned.
func (d *Device) FrameSize() (w, h int) { return d.Size() }

// Rotated is false: the canvas is the way up the app shows it.
func (d *Device) Rotated() bool { return false }

// PixFmt is the canvas's own byte order.
func (d *Device) PixFmt() string { return "rgba" }

// PresentFrame is refused: a panel carries no video decoder (video.Installed is false), so nothing
// makes a frame for it.
func (d *Device) PresentFrame([]byte, image.Rectangle) error {
	return fmt.Errorf("screen: no video frames on a panel")
}

// Close lets the app's surface go. The connection stays for the touchscreen until it fails.
func (d *Device) Close() error { return nil }

var (
	lightMu sync.Mutex
	light   = BacklightMax
)

// SetBacklight asks the app for a brightness, 0 (screen off) to BacklightMax.
func SetBacklight(level int) error {
	level = min(max(level, 0), BacklightMax)
	c, err := panelscreen.Get()
	if err != nil {
		return fmt.Errorf("screen: backlight: %w", err)
	}
	if err := c.Backlight(level); err != nil {
		panelscreen.Drop(c)
		return fmt.Errorf("screen: backlight: %w", err)
	}
	lightMu.Lock()
	light = level
	lightMu.Unlock()
	return nil
}

// Backlight is the level last asked for; the app does not report one of its own.
func Backlight() (int, error) {
	lightMu.Lock()
	defer lightMu.Unlock()
	return light, nil
}
