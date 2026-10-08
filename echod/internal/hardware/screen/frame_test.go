//go:build !dot && !spot

package screen

import (
	"bytes"
	"image"
	"image/color"
	"math/rand"
	"testing"
)

// A frame lands on the panel as it is, row by row into a padded framebuffer, and only the canvas's
// rectangle is drawn over it, at the place the canvas has it once turned.
func TestAFrameIsCopiedAndTheControlsDrawnOverIt(t *testing.T) {
	d, dst := fake([4]uint{16, 8, 0, 24}) // BGRA, as the Show's panel is
	if d.PixFmt() != "bgra" {
		t.Fatalf("pixfmt %q", d.PixFmt())
	}
	w, h := d.FrameSize()
	frame := make([]byte, w*h*4)
	rand.New(rand.NewSource(2)).Read(frame)

	if err := d.paintFrame(dst, 0, frame, image.Rectangle{}); err != nil {
		t.Fatal(err)
	}
	for py := 0; py < h; py++ {
		if !bytes.Equal(dst[py*d.line:py*d.line+w*4], frame[py*w*4:(py+1)*w*4]) {
			t.Fatalf("row %d is not the frame's", py)
		}
	}

	// The controls: an opaque red pixel and a half-covering black one in the strip, and a pixel outside
	// it that must not be drawn.
	d.canvas.SetRGBA(10, 470, color.RGBA{R: 255, A: 255})
	d.canvas.SetRGBA(11, 470, color.RGBA{A: 128})
	d.canvas.SetRGBA(10, 100, color.RGBA{G: 255, A: 255})
	strip := image.Rect(0, 400, 960, 480)
	if err := d.paintFrame(dst, 0, frame, strip); err != nil {
		t.Fatal(err)
	}
	at := func(py, y int) []byte { // panel row py, canvas row y: panel column 479-y
		i := py*d.line + (w-1-y)*4
		return dst[i : i+4]
	}
	src := func(py, y int) []byte {
		i := py*w*4 + (w-1-y)*4
		return frame[i : i+4]
	}
	if p := at(10, 470); p[0] != 0 || p[1] != 0 || p[2] != 255 {
		t.Errorf("the red pixel is %v", p)
	}
	if p, f := at(11, 470), src(11, 470); p[0] != uint8(uint32(f[0])*127/255) || p[2] != uint8(uint32(f[2])*127/255) {
		t.Errorf("the half-covering pixel is %v over %v", p, f)
	}
	if !bytes.Equal(at(10, 100), src(10, 100)) {
		t.Error("a pixel outside the rectangle was drawn")
	}
	if !bytes.Equal(at(500, 300), src(500, 300)) {
		t.Error("a pixel the canvas left clear changed")
	}
}

// The page a frame went onto is drawn whole by the next Present, though the canvas matches what the
// page held before: its shadow is no longer the truth.
func TestAPageAFrameWentOnIsDrawnWholeNextTime(t *testing.T) {
	d, dst := fake([4]uint{16, 8, 0, 24})
	rand.New(rand.NewSource(3)).Read(d.canvas.Pix)
	d.rotate(dst, 0)
	want := append([]byte(nil), dst...)

	w, h := d.FrameSize()
	if err := d.paintFrame(dst, 0, make([]byte, w*h*4), image.Rectangle{}); err != nil {
		t.Fatal(err)
	}
	d.rotate(dst, 0)
	if !bytes.Equal(dst, want) {
		t.Error("the canvas was not drawn back over the frame")
	}
}

func TestAShortFrameIsRefused(t *testing.T) {
	d, dst := fake([4]uint{16, 8, 0, 24})
	if err := d.paintFrame(dst, 0, make([]byte, 100), image.Rectangle{}); err == nil {
		t.Error("a short frame was taken")
	}
}

func BenchmarkPaintFrame(b *testing.B) {
	d, dst := fake([4]uint{16, 8, 0, 24})
	d.line = 480 * 4
	w, h := d.FrameSize()
	frame := make([]byte, w*h*4)
	b.SetBytes(int64(len(frame)))
	for i := 0; i < b.N; i++ {
		_ = d.paintFrame(dst, 0, frame, image.Rectangle{})
	}
}

func BenchmarkPaintFrameWithControls(b *testing.B) {
	d, dst := fake([4]uint{16, 8, 0, 24})
	w, h := d.FrameSize()
	frame := make([]byte, w*h*4)
	strip := image.Rect(0, 380, 960, 480)
	for y := 380; y < 480; y++ {
		for x := 0; x < 960; x++ {
			d.canvas.SetRGBA(x, y, color.RGBA{A: 160})
		}
	}
	b.SetBytes(int64(len(frame)))
	for i := 0; i < b.N; i++ {
		_ = d.paintFrame(dst, 0, frame, strip)
	}
}
