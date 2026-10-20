//go:build !dot && !panel

package screen

import (
	"image"
	"testing"
)

// On the framebuffer a tint is drawn into a copy of the frame: the canvas keeps what was drawn on it,
// so a frame drawn once and shown again is not tinted twice.
func TestTintLeavesTheCanvasAsDrawn(t *testing.T) {
	d := &Device{canvas: image.NewRGBA(image.Rect(0, 0, 2, 1))}
	copy(d.canvas.Pix, []byte{200, 100, 50, 255, 255, 255, 255, 255})
	d.SetTint(Tint{1, 0.5, 0.25})
	for range 2 {
		got := d.tinted().Pix
		want := []byte{200, 50, 13, 255, 255, 128, 64, 255}
		if string(got) != string(want) {
			t.Fatalf("tinted %v, want %v", got, want)
		}
	}
	if d.canvas.Pix[1] != 100 {
		t.Fatalf("canvas changed: %v", d.canvas.Pix)
	}
	d.SetTint(None)
	if d.tintTable != nil {
		t.Fatal("no tint still has a table")
	}
}
