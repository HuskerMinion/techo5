//go:build !dot && !spot

package display

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/makiuchi-d/gozxing"
	"rsc.io/qr"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
)

// qrPictureOf is a camera-sized picture with text as a QR code in the middle of it, on grey, about as
// large as one held up a hand's width from the camera.
func qrPictureOf(t *testing.T, text string) *image.RGBA {
	t.Helper()
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		t.Fatal(err)
	}
	pic := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	draw.Draw(pic, pic.Bounds(), image.NewUniform(color.RGBA{0x80, 0x80, 0x80, 0xFF}), image.Point{}, draw.Src)
	const scale, quiet = 6, 4
	n := code.Size
	at := image.Pt((1280-(n+2*quiet)*scale)/2, (720-(n+2*quiet)*scale)/2)
	draw.Draw(pic, image.Rect(at.X, at.Y, at.X+(n+2*quiet)*scale, at.Y+(n+2*quiet)*scale), image.White, image.Point{}, draw.Src)
	for y := range n {
		for x := range n {
			if code.Black(x, y) {
				r := image.Rect(at.X+(x+quiet)*scale, at.Y+(y+quiet)*scale, at.X+(x+quiet+1)*scale, at.Y+(y+quiet+1)*scale)
				draw.Draw(pic, r, image.Black, image.Point{}, draw.Src)
			}
		}
	}
	return pic
}

// standInScanCamera stands in a camera for the scan that hands every listener a frame with pic in it
// every few milliseconds, and records whether it was let go.
func standInScanCamera(t *testing.T, pic func() *image.RGBA) (released func() bool) {
	t.Helper()
	acquire, listen, empty, picture, every := qrScanAcquire, qrScanListen, qrEmpty, qrPicture, qrScanEvery
	t.Cleanup(func() {
		qrScanAcquire, qrScanListen, qrEmpty, qrPicture, qrScanEvery = acquire, listen, empty, picture, every
	})
	var mu sync.Mutex
	let := false
	qrScanAcquire = func() (func(), error) {
		return func() { mu.Lock(); let = true; mu.Unlock() }, nil
	}
	qrScanListen = func(fn func(*camera.Frame)) func() {
		stop, stopped := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(stopped)
			for {
				select {
				case <-stop:
					return
				case <-time.After(5 * time.Millisecond):
					fn(&camera.Frame{At: time.Now()})
				}
			}
		}()
		var once sync.Once
		return func() { once.Do(func() { close(stop) }); <-stopped }
	}
	qrEmpty = func(*camera.Frame) bool { return false }
	qrPicture = func(*camera.Frame) *image.RGBA { return pic() }
	qrScanEvery = 10 * time.Millisecond
	return func() bool { mu.Lock(); defer mu.Unlock(); return let }
}

func scanDisplay() *Display {
	return &Display{r: newRenderer(image.NewRGBA(image.Rect(0, 0, 960, 480))), poke: make(chan struct{}, 1)}
}

// A code held up is read, the page says so and closes, and the camera is let go.
func TestAQRScanReadsTheCodeHeldUp(t *testing.T) {
	const text = "MT:Y.K9042C00KA0648G00" // made up, in a Matter code's form
	pic := qrPictureOf(t, text)
	released := standInScanCamera(t, func() *image.RGBA { return pic })
	found := qrFoundFor
	qrFoundFor = 10 * time.Millisecond
	t.Cleanup(func() { qrFoundFor = found })

	d := scanDisplay()
	code, err := d.ScanQR(context.Background(), 5*time.Second)
	if err != nil || code != text {
		t.Fatalf("scan = %q, %v; want %q", code, err, text)
	}
	if d.qrScanUp() {
		t.Error("the page is still up after the scan")
	}
	if !released() {
		t.Error("the camera was not let go")
	}
}

// Nothing to read within the time is ErrNothingScanned, and so is Cancel; one scan at a time.
func TestAQRScanEndsWithNothingReadOrCanceled(t *testing.T) {
	blank := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	standInScanCamera(t, func() *image.RGBA { return blank })

	d := scanDisplay()
	if _, err := d.ScanQR(context.Background(), 100*time.Millisecond); !errors.Is(err, ErrNothingScanned) {
		t.Fatalf("nothing to read: err = %v, want ErrNothingScanned", err)
	}

	done := make(chan error, 1)
	go func() { _, err := d.ScanQR(context.Background(), 5*time.Second); done <- err }()
	for end := time.Now().Add(2 * time.Second); !d.qrScanUp(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("the page never came up")
		}
	}
	if _, err := d.ScanQR(context.Background(), time.Second); err == nil || errors.Is(err, ErrNothingScanned) {
		t.Errorf("a second scan while one is up: err = %v, want it refused", err)
	}
	d.qrScanTap(d.r.w-5, 5) // off Cancel: nothing
	if !d.qrScanUp() {
		t.Fatal("a tap off Cancel closed the page")
	}
	c := d.r.qrCancelRect()
	d.qrScanTap(c.Min.X+c.Dx()/2, c.Min.Y+c.Dy()/2)
	select {
	case err := <-done:
		if !errors.Is(err, ErrNothingScanned) {
			t.Errorf("canceled: err = %v, want ErrNothingScanned", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Cancel did not end the scan")
	}
}

// The page in each of its states, in English and German. With SHOW_PREVIEW set, each is written there
// to look at.
func TestTheQRScanPageDraws(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	dir := os.Getenv("SHOW_PREVIEW")
	at := time.Date(2026, 11, 11, 18, 30, 0, 0, time.Local)
	pic := qrPictureOf(t, "MT:Y.K9042C00KA0648G00")
	for _, lang := range []string{"", "de"} {
		if err := config.Set().Screen().Language(lang); err != nil {
			t.Fatal(err)
		}
		for name, v := range map[string]qrScanView{
			"starting": {left: 20 * time.Second},
			"scanning": {left: 12 * time.Second, started: true, w: 1280, h: 720, preview: pic},
			"read":     {started: true, w: 1280, h: 720, preview: pic, code: "MT:Y.K9042C00KA0648G00"},
			"muted":    {left: 20 * time.Second, empty: true, started: true},
		} {
			img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
			newRenderer(img).draw(scene{now: at, phase: "idle", qrscan: &v})
			if dir == "" {
				continue
			}
			f, err := os.Create(filepath.Join(dir, "qrscan-"+name+"-"+cmp.Or(lang, "en")+".png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, img); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
}

// The levels read straight off a picture's pixels are gozxing's own, level for level, for a whole
// picture and for a part of one, so a code is read on the same frames as before.
func TestQRLuminanceMatchesGozxings(t *testing.T) {
	pic := image.NewRGBA(image.Rect(0, 0, 64, 48))
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range pic.Pix {
		pic.Pix[i] = uint8(rng.UintN(256))
		if i%4 == 3 {
			pic.Pix[i] = 255
		}
	}
	for name, img := range map[string]*image.RGBA{
		"whole": pic,
		"part":  pic.SubImage(image.Rect(5, 7, 50, 40)).(*image.RGBA),
	} {
		got, want := luminance(img).GetMatrix(), gozxing.NewLuminanceSourceFromImage(img).GetMatrix()
		if !bytes.Equal(got, want) {
			t.Errorf("%s: the levels differ from gozxing's", name)
		}
	}
}
