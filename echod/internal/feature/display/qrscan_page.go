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
	"github.com/HuskerMinion/techo5/echod/internal/hardware/lenscover"
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
	qrCovered     = func() bool { return lenscover.Get().Covered() }
	qrMuted       = func() bool { muted, _ := mute.Get().Muted(); return muted }
)

// qrBlockedEvery is how often a scan that waits for the camera looks again whether the shutter has
// been opened, or the privacy mute turned off.
var qrBlockedEvery = 300 * time.Millisecond

// qrCancelAfter is how soon after the page opens Cancel takes a tap. Cancel is where the button that
// opened it was, so a second tap on that button while the page comes up does not close it again.
var qrCancelAfter = 800 * time.Millisecond

// ErrNothingScanned is a scan that ran out of time, or was canceled on the screen, without a code.
var ErrNothingScanned = errors.New("qr scan: no code was read")

// qrScan is one opening of the page. The frames arrive on the camera's goroutine and are read on one
// of their own, so what they write is under mu, not the display's lock.
type qrScan struct {
	opened time.Time
	until  time.Time
	done   chan struct{} // closed when the page is taken down
	once   sync.Once
	found  chan string // the code, once

	// What the frames are read with, taken when the scan starts: a frame still being read after it
	// ended uses what it began with.
	every   time.Duration
	isEmpty func(*camera.Frame) bool
	picture func(*camera.Frame) *image.RGBA
	read    func(image.Image) (string, bool)
	look    qrLook

	mu      sync.Mutex
	blocked string // while the scan waits for the camera: "cover" (the shutter is closed) or "mute"
	started bool   // a frame has come
	empty   bool   // the last frame had nothing in it: the camera is held off
	reading bool   // a frame is being read
	readAt  time.Time
	w, h    int
	preview *image.RGBA
	code    string // what was read
	said    string // what the page says of it until it closes
}

// qrLook is what a scan opened for something on the device itself looks for and says: the Wi-Fi page's
// scan reads only a Wi-Fi code, and never shows the passphrase in it. The zero qrLook is Home
// Assistant's scan, which takes any code and shows it.
type qrLook struct {
	title  string                 // the page's title; empty is "Scan a QR code"
	accept func(code string) bool // whether a code read ends the scan; nil takes any
	say    func(code string) string
	// wait is a scan that waits, saying why on the page, while the shutter is closed or the privacy
	// mute is on, and starts once the camera can be had; without it the scan ends at once with the
	// camera's refusal.
	wait bool
}

// saying is what the page says of a code read.
func (l qrLook) saying(code string) string {
	if l.say != nil {
		return l.say(code)
	}
	return fmt.Sprintf(tr("qr_scan_read"), code)
}

// qrScanView is the page as one frame draws it.
type qrScanView struct {
	title   string
	blocked string
	left    time.Duration
	started bool
	empty   bool
	w, h    int
	preview *image.RGBA
	code    string
	said    string
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
		if code != "" && q.look.accept != nil && !q.look.accept(code) {
			code = "" // not the code this scan is for: keep looking
		}
		said := ""
		if code != "" {
			said = q.look.saying(code)
		}
		q.mu.Lock()
		if img != nil {
			q.preview = img
			q.w, q.h = img.Bounds().Dx(), img.Bounds().Dy()
		}
		first := code != "" && q.code == ""
		if first {
			q.code, q.said = code, said
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
		title: q.look.title, said: q.said, blocked: q.blocked,
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
	return d.scanQR(ctx, within, qrLook{})
}

// scanQR is ScanQR looking for, and saying, what look says.
func (d *Display) scanQR(ctx context.Context, within time.Duration, look qrLook) (string, error) {
	now := time.Now()
	q := &qrScan{
		opened: now, until: now.Add(within), done: make(chan struct{}), found: make(chan string, 1),
		every: qrScanEvery, isEmpty: qrEmpty, picture: qrPicture, read: qrRead, look: look,
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

	ctx, stop := context.WithDeadline(ctx, q.until)
	defer stop()
	release, err := d.qrScanCamera(ctx, q)
	if err != nil {
		return "", err
	}
	defer release()
	cancel := qrScanListen(func(fr *camera.Frame) { q.frame(fr, d.wake) })
	defer cancel()

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

// qrScanCamera holds the camera for q. A scan that waits (qrLook.wait) says on the page that the
// shutter is closed, or the privacy mute on, and tries again until the camera can be had, the time
// runs out, or Cancel is pressed; any other scan has the camera's refusal at once.
func (d *Display) qrScanCamera(ctx context.Context, q *qrScan) (func(), error) {
	for {
		release, err := qrScanAcquire()
		blocked := ""
		switch {
		case err == nil:
		case qrMuted():
			blocked, err = "mute", errors.New("qr scan: the privacy mute holds the camera off")
		case qrCovered():
			blocked = "cover"
		}
		if err == nil || blocked == "" || !q.look.wait {
			if err != nil {
				slog.Info("qr scan: no camera", "err", err)
			}
			q.setBlocked("")
			return release, err
		}
		if q.setBlocked(blocked) {
			slog.Info("qr scan: waiting for the camera", "blocked", blocked)
			d.wake()
		}
		select {
		case <-q.done:
			slog.Info("qr scan: canceled on the screen")
			return nil, ErrNothingScanned
		case <-ctx.Done():
			slog.Info("qr scan: the camera was not opened", "blocked", blocked)
			return nil, ErrNothingScanned
		case <-time.After(qrBlockedEvery):
		}
	}
}

// setBlocked says what the scan waits on, "" for nothing, and whether that changed.
func (q *qrScan) setBlocked(b string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	changed := q.blocked != b
	q.blocked = b
	return changed
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
	if q != nil && time.Since(q.opened) >= qrCancelAfter {
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
	title := v.title
	if title == "" {
		title = tr("qr_scan_title")
	}
	r.text(r.body, title, r.margin, r.s(52), amber)
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
		msg = v.said
		if msg == "" {
			msg = qrLook{}.saying(v.code)
		}
	case v.blocked != "":
		// Waiting for the camera: said large in the middle of the picture's place, as it stays so
		// until somebody opens the shutter or turns the mute off.
		r.bevel(pic, shift(walnut, 8), false)
		why := tr("qr_scan_covered")
		if v.blocked == "mute" {
			why = tr("qr_scan_muted")
		}
		lines := r.wrap(r.body, why, pic.Dx()-r.s(48))
		lh := r.body.Metrics().Height.Ceil()
		y := pic.Min.Y + (pic.Dy()-len(lines)*lh)/2 + r.body.Metrics().Ascent.Ceil()
		for _, l := range lines {
			r.text(r.body, l, pic.Min.X+(pic.Dx()-r.width(r.body, l))/2, y, amber)
			y += lh
		}
		r.labelButton(r.qrCancelRect(), tr("cancel"), btnSecondary)
		return
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
