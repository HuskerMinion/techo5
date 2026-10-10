//go:build !dot && !spot

package display

import (
	"image"
	"testing"
	"time"
)

// Every key, field and mode button of the network address page is where a finger lands on it, at the
// Show 5's size and the Show 8's: what is drawn and what a tap means come from the same rectangles.
func TestTheAddressPageIsTappedWhereItIsDrawn(t *testing.T) {
	for _, size := range []image.Point{{960, 480}, {1280, 800}} {
		img := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
		r := newRenderer(img)
		f := &addrForm{fixed: true, field: addrFieldGateway}
		f.vals[addrFieldAddress], f.vals[addrFieldGateway] = "192.168.1.50/24", "192.168.1.1"
		r.addressPage(scene{now: time.Date(2026, 10, 8, 9, 41, 0, 0, time.UTC), wifi: wifiState{addr: f}})

		center := func(rc image.Rectangle) (int, int) { return rc.Min.X + rc.Dx()/2, rc.Min.Y + rc.Dy()/2 }
		auto, fixed := r.addrModeRects()
		for want, rc := range map[string]image.Rectangle{"auto": auto, "fixed": fixed} {
			if got := r.addrHit(center(rc)); got != want {
				t.Errorf("%v: the %s button reads as %q", size, want, got)
			}
		}
		for i := 0; i < addrFields; i++ {
			want := "field:" + string(rune('0'+i))
			if got := r.addrHit(center(r.addrFieldRect(i))); got != want {
				t.Errorf("%v: the %s field reads as %q", size, addrLabels[i], got)
			}
		}
		for row := range addrKeys {
			for i, k := range addrKeys[row] {
				rc := r.addrKeyRect(row, i)
				if rc.Max.X > size.X || rc.Max.Y > size.Y {
					t.Errorf("%v: the %s key runs off the screen at %v", size, k, rc)
				}
				if got := r.addrHit(center(rc)); got != k {
					t.Errorf("%v: the %s key reads as %q", size, k, got)
				}
			}
		}
		// Between the fields and the keypad is the status line, which is no key.
		if got := r.addrHit(size.X/2, r.s(addrStatusTopBase)-r.s(4)); got != "" {
			t.Errorf("%v: the status line reads as %q", size, got)
		}
	}
}
