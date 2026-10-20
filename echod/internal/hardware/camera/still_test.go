//go:build !dot && !spot && !panel

package camera

import "testing"

// A still from the whole array develops at its own size, whatever size the stream is.
func TestStillDevelopsAtItsOwnSize(t *testing.T) {
	g := geometry{w: 4224, h: 3136, bpl: 4224 * 10 / 8}
	raw := make([]byte, g.bpl*g.h)
	for i := range raw {
		raw[i] = 0x55
	}
	f := &Frame{raw: raw, full: &g}
	if img := f.Full(); img.Bounds().Dx() != g.w || img.Bounds().Dy() != g.h {
		t.Fatalf("still developed at %v, want %dx%d", img.Bounds(), g.w, g.h)
	}
	if got := geometryOf(&Frame{}); got != streamed() {
		t.Fatalf("a streamed frame has geometry %+v, want the stream's %+v", got, streamed())
	}
}
