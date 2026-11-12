//go:build !dot && !spot

package display

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"log/slog"
	"sync"
	"time"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	xdraw "golang.org/x/image/draw"

	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// The QR scan: a page Home Assistant opens (the camera's qr_scan action) to read a QR code held up to
// the camera, such as a Matter device's setup code. It shows the camera's picture, so whoever holds
// the code sees where it is, reads every picture it shows, and closes as soon as one has a code in it,
// after a moment saying so. It holds the camera only while it is up, and Cancel takes it down.

var (
	// qrScanEvery is how often a frame is developed, shown and read, one at a time. A full-size frame
	// takes about 140 ms on a Show 5 from the sensor to an answer, so this leaves a core free.
	qrScanEvery = 150 * time.Millisecond

	// qrFoundFor is how long the page says what it read before it closes.
	qrFoundFor = 1500 * time.Millisecond
)

// What the page holds the camera through and reads with. Variables so that a test can stand in a
// camera on a machine with none.
var (
	qrScanAcquire = func() (func(), error) { return camera.Get().Acquire() }
	qrScanListen  = func(fn func(*camera.Frame)) func() { return camera.Get().Frames.Listen(fn) }
	qrRead        = readQR
	qrEmpty       = func(fr *camera.Frame) bool { w, _ := fr.Dims(); return w == 0 }
	qrPicture     = func(fr *camera.Frame) *image.RGBA { return fr.Full() }
)

// ErrNothingScanned is a scan that ran out of time, or was canceled on the screen, without a code.
var ErrNothingScanned = errors.New("qr scan: no code was read")

// qrScan is one opening of the page. The frames arrive on the camera's goroutine and are read on one
// of their own, so what they write is under mu, not the display's lock.
type qrScan struct {
	until time.Time
	done  chan struct{} // closed when the page is taken down
	once  sync.Once
	found chan string // the code, once

	// What the frames are read with, taken when the scan starts: a frame still being read after it
	// ended uses what it began with.
	every   time.Duration
	isEmpty func(*camera.Frame) bool
	picture func(*camera.Frame) *image.RGBA
	read    func(image.Image) (string, bool)

	mu      sync.Mutex
	started bool // a frame has come
	empty   bool // the last frame had nothing in it: the camera is held off
	reading bool // a frame is being read
	readAt  time.Time
	w, h    int
	preview *image.RGBA
	code    string // what was read, shown until the page closes
}

// qrScanView is the page as one frame draws it.
type qrScanView struct {
	left    time.Duration
	started bool
	empty   bool
	w, h    int
	preview *image.RGBA
	code    string
}

func (q *qrScan) stop() { q.once.Do(func() { close(q.done) }) }

// frame is one frame from the sensor: every qrScanEvery one is developed at full size, shown and read,
// on a goroutine of its own, since a listener must not hold the camera up.
func (q *qrScan) frame(fr *camera.Frame, wake func()) {
	now := time.Now()
	q.mu.Lock()
	defer q.mu.Unlock()
	q.started = true
	q.empty = q.isEmpty(fr)
	if q.empty || q.code != "" || q.reading || now.Sub(q.readAt) < q.every {
		return
	}
	q.reading, q.readAt = true, now
	safe.Go("qr scan read", func() {
		// A read that panics (safe.Go recovers it) must still let the next frame be read.
		defer func() {
			q.mu.Lock()
			q.reading = false
			q.mu.Unlock()
		}()
		img := q.picture(fr)
		code := ""
		if img != nil {
			code, _ = q.read(img)
		}
		q.mu.Lock()
		if img != nil {
			q.preview = img
			q.w, q.h = img.Bounds().Dx(), img.Bounds().Dy()
		}
		first := code != "" && q.code == ""
		if first {
			q.code = code
		}
		q.mu.Unlock()
		if first {
			q.found <- code
		}
		wake()
	})
}

func (q *qrScan) view(now time.Time) qrScanView {
	q.mu.Lock()
	defer q.mu.Unlock()
	return qrScanView{
		left: q.until.Sub(now), started: q.started, empty: q.empty,
		w: q.w, h: q.h, preview: q.preview, code: q.code,
	}
}

// readQR is the text of the QR code in img, if it has one.
func readQR(img image.Image) (string, bool) {
	bmp, err := gozxing.NewBinaryBitmap(gozxing.NewHybridBinarizer(luminance(img)))
	if err != nil {
		return "", false
	}
	hints := map[gozxing.DecodeHintType]interface{}{gozxing.DecodeHintType_TRY_HARDER: true}
	res, err := qrcode.NewQRCodeReader().Decode(bmp, hints)
	if err != nil || res.GetText() == "" {
		return "", false
	}
	return res.GetText(), true
}

// luminance is img in the gray levels the reader works on. gozxing's own conversion reads a picture
// one pixel at a time through image.Image's At, about 280 ms for a full-size frame on a Show 5; for the
// camera's RGBA pictures the same levels come straight off the pixels in under 20.
func luminance(img image.Image) gozxing.LuminanceSource {
	rgba, ok := img.(*image.RGBA)
	if !ok {
		return gozxing.NewLuminanceSourceFromImage(img)
	}
	b := rgba.Bounds()
	w, h := b.Dx(), b.Dy()
	gray := make([]byte, w*h)
	for y := range h {
		row := rgba.Pix[y*rgba.Stride : y*rgba.Stride+w*4]
		for x := range w {
			p := row[x*4 : x*4+3]
			gray[y*w+x] = byte((uint32(p[0]) + 2*uint32(p[1]) + uint32(p[2])) / 4)
		}
	}
	src, err := gozxing.NewPlanarYUVLuminanceSource(gray, w, h, 0, 0, w, h, false)
	if err != nil {
		return gozxing.NewLuminanceSourceFromImage(img)
	}
	return src
}

// ScanQR puts the page up and answers with the first QR code the camera reads within the time given.
// It answers ErrNothingScanned when the time runs out, or Cancel is pressed, first; and the camera's
// own refusal (the privacy mute, a closed shutter) at once. One scan at a time.
func (d *Display) ScanQR(ctx context.Context, within time.Duration) (string, error) {
	q := &qrScan{
		until: time.Now().Add(within), done: make(chan struct{}), found: make(chan string, 1),
		every: qrScanEvery, isEmpty: qrEmpty, picture: qrPicture, read: qrRead,
	}
	d.mu.Lock()
	if d.qrscan != nil {
		d.mu.Unlock()
		return "", errors.New("qr scan: a scan is already on the screen")
	}
	d.qrscan = q
	d.mu.Unlock()
	defer d.endQRScan(q)
	slog.Info("qr scan", "open", true, "for", within)
	d.wake()

	release, err := qrScanAcquire()
	if err != nil {
		if muted, _ := mute.Get().Muted(); muted {
			err = errors.New("qr scan: the privacy mute holds the camera off")
		}
		slog.Info("qr scan: no camera", "err", err)
		return "", err
	}
	defer release()
	cancel := qrScanListen(func(fr *camera.Frame) { q.frame(fr, d.wake) })
	defer cancel()

	ctx, stop := context.WithDeadline(ctx, q.until)
	defer stop()
	tick := time.NewTicker(time.Second) // the countdown
	defer tick.Stop()
	for {
		select {
		case code := <-q.found:
			slog.Info("qr scan: read a code", "length", len(code))
			select { // say so, unless Cancel comes first
			case <-time.After(qrFoundFor):
			case <-q.done:
			}
			return code, nil
		case <-q.done:
			slog.Info("qr scan: canceled on the screen")
			return "", ErrNothingScanned
		case <-ctx.Done():
			slog.Info("qr scan: nothing read", "for", within)
			return "", ErrNothingScanned
		case <-tick.C:
			d.wake()
		}
	}
}

// endQRScan takes the page down if q is still the one up.
func (d *Display) endQRScan(q *qrScan) {
	d.mu.Lock()
	if d.qrscan == q {
		d.qrscan = nil
	}
	d.mu.Unlock()
	q.stop()
	slog.Info("qr scan", "open", false)
	d.wake()
}

func (d *Display) qrScanUp() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.qrscan != nil
}

// qrScanView is the page for the frame being drawn; nil while it is down.
func (d *Display) qrScanView(now time.Time) *qrScanView {
	d.mu.Lock()
	q := d.qrscan
	d.mu.Unlock()
	if q == nil {
		return nil
	}
	v := q.view(now)
	return &v
}

// qrScanTap is a finger on the page: only Cancel means anything.
func (d *Display) qrScanTap(x, y int) {
	if !image.Pt(x, y).In(d.r.qrCancelRect()) {
		return
	}
	d.mu.Lock()
	q := d.qrscan
	d.mu.Unlock()
	if q != nil {
		q.stop()
	}
}

// qrCancelRect is Cancel, at the bottom in the middle.
func (r *renderer) qrCancelRect() image.Rectangle {
	bottom := r.h - r.s(camTestBottomBase)
	w := r.s(camTestBackWBase)
	return image.Rect((r.w-w)/2, bottom-r.s(camTestBackHBase), (r.w+w)/2, bottom)
}

// qrScanPage draws the page: the picture as large as fits between the title and Cancel, with what to
// do, or what was read, over its foot.
func (r *renderer) qrScanPage(s scene) {
	v := s.qrscan
	r.text(r.body, tr("qr_scan_title"), r.margin, r.s(52), amber)
	right := clockHM(s.now)
	if v.left > 0 && v.code == "" {
		right = fmt.Sprintf("%d s  ·  %s", int(v.left.Round(time.Second).Seconds()), right)
	}
	r.text(r.small, right, r.w-r.margin-r.width(r.small, right), r.s(52), dim)

	top, bottom := r.s(camTestTopBase), r.qrCancelRect().Min.Y-r.s(14)
	box := image.Rect(r.margin, top, r.w-r.margin, bottom)
	pic := fitIn(box, v.w, v.h)

	msg := tr("qr_scan_hint")
	switch {
	case v.code != "":
		msg = fmt.Sprintf(tr("qr_scan_read"), v.code)
	case v.empty:
		msg = tr("qr_scan_muted")
	case v.preview == nil:
		msg = tr("ui_starting")
	}
	if v.preview != nil && !v.empty {
		xdraw.ApproxBiLinear.Scale(r.dst, pic, v.preview, v.preview.Bounds(), draw.Src, nil)
	} else {
		r.bevel(pic, shift(walnut, 8), false)
	}
	lines := r.wrap(r.small, msg, pic.Dx()-r.s(32))
	if len(lines) > 3 {
		// A long code (a QR code can hold a few thousand characters) is cut to three lines, so the band
		// stays on the picture; the whole of it goes to Home Assistant.
		lines = append(lines[:2:2], lines[2]+"…")
	}
	lh := r.small.Metrics().Height.Ceil()
	band := image.Rect(pic.Min.X, pic.Max.Y-len(lines)*lh-r.s(20), pic.Max.X, pic.Max.Y)
	draw.Draw(r.dst, band, image.NewUniform(shift(walnut, 4)), image.Point{}, draw.Src)
	y := band.Min.Y + r.s(10) + r.small.Metrics().Ascent.Ceil()
	c := cream
	if v.code != "" {
		c = amber
	}
	for _, l := range lines {
		r.text(r.small, l, pic.Min.X+(pic.Dx()-r.width(r.small, l))/2, y, c)
		y += lh
	}
	r.labelButton(r.qrCancelRect(), tr("cancel"), btnSecondary)
}
