package camera

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/jpegenc"
)

// fakeFrame is a frame's two developments: the half-size picture and the full-size one.
type fakeFrame struct{ half, full *image.RGBA }

func (f fakeFrame) Image() *image.RGBA { return f.half }
func (f fakeFrame) Full() *image.RGBA  { return f.full }

func picture(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = uint8(i * 7)
	}
	return img
}

// Every board but the 2nd gen Show 5 on a mainline kernel and the 2nd gen Show 8 with its JPEG encoder
// keeps the camera pages as they were: the stream is the half-size picture and the still the full-size
// one, each a single encode at quality 85.
func TestOtherBoardsKeepHalfSizePagesAt85(t *testing.T) {
	if fullPagesOn(false, false) {
		t.Fatal("a board with neither the Show 5's mainline kernel nor the Show 8's JPEG encoder sends full-size pages")
	}
	if !fullPagesOn(true, false) || !fullPagesOn(false, true) {
		t.Fatal("the Show 5 on mainline, or the Show 8 with its JPEG encoder, does not send full-size pages")
	}
	if hwEncode {
		t.Fatal("the JPEG encoder is used on a board that is not the 2nd gen Show 8 on its mainline kernel")
	}
	was := fullPages
	fullPages = false
	t.Cleanup(func() { fullPages = was })
	if quality != 85 {
		t.Fatalf("quality %d, want 85", quality)
	}
	f := fakeFrame{half: picture(800, 600), full: picture(1600, 1200)}
	for _, c := range []struct {
		name string
		enc  func() ([]byte, error)
		img  *image.RGBA
	}{
		{"stream", func() ([]byte, error) { return encodeHalf(f) }, f.half},
		{"still", func() ([]byte, error) { return encodeFull(f) }, f.full},
	} {
		got, err := c.enc()
		if err != nil {
			t.Fatal(err)
		}
		var want bytes.Buffer
		if err := jpeg.Encode(&want, c.img, &jpeg.Options{Quality: 85}); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want.Bytes()) {
			t.Errorf("%s: not the single encode at 85 of the %v picture", c.name, c.img.Bounds().Size())
		}
	}
}

// Where the JPEG encoder makes the full-size pages, they go at most encoderFPS a second at
// qualityEncoder; elsewhere a page stream is not paced, as before.
func TestEncoderPagesArePaced(t *testing.T) {
	if pageInterval(false) != 0 {
		t.Errorf("a board without the encoder paces its pages: %v", pageInterval(false))
	}
	if got := pageInterval(true); got != time.Second/encoderFPS || encoderFPS != 7 {
		t.Errorf("with the encoder, a picture every %v, want every %v", got, time.Second/7)
	}
	if qualityEncoder != 80 || qualityFull != 90 {
		t.Errorf("qualities %d by the encoder and %d on the cores, want 80 and 90", qualityEncoder, qualityFull)
	}
	now := time.Now()
	interval := pageInterval(true)
	if pageDue(now, now.Add(interval/2), interval, false) {
		t.Error("a frame half an interval after the last went out")
	}
	if !pageDue(now, now.Add(interval/2), interval, true) {
		t.Error("the black frame of a stopped sensor was held back")
	}
	if !pageDue(now, now.Add(interval), interval, false) || !pageDue(time.Time{}, now, interval, false) {
		t.Error("a frame due, or the first, was held back")
	}
	if !pageDue(now, now, 0, false) {
		t.Error("a page without pacing held a frame back")
	}
}

// A failing encoder is logged once a minute at most, and the pictures jpegenc refuses while it backs off
// are not logged at all.
func TestHardwareFailuresLoggedOnceAMinute(t *testing.T) {
	was := hwWarned.Load()
	t.Cleanup(func() { hwWarned.Store(was) })
	hwWarned.Store(0)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	failure := errors.New("jpegenc: no jpeg within 2s")
	if !hwFailure(failure, at) {
		t.Fatal("the first failure was not logged")
	}
	if hwFailure(failure, at.Add(30*time.Second)) || hwFailure(failure, at.Add(time.Minute-time.Millisecond)) {
		t.Error("a second failure within the minute was logged")
	}
	if hwFailure(fmt.Errorf("%w until 12:01:00", jpegenc.ErrBackingOff), at.Add(2*time.Minute)) {
		t.Error("a picture refused while backing off was logged")
	}
	if !hwFailure(failure, at.Add(time.Minute)) {
		t.Error("a failure a minute on was not logged")
	}
}
