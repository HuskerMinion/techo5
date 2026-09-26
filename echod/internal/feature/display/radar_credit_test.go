//go:build !dot && !spot

package display

import (
	"image"
	"testing"
)

// The rain map's credit line fits the page on both panels, whole: it carries GeoNames' CC BY credit,
// last. The string is home's widestRadarCredit, which home's TestWidestRadarCredit keeps current.
func TestRadarCreditFits(t *testing.T) {
	const widest = "Radar RainViewer · Clouds NOAA · Map NASA · Places GeoNames"
	for _, p := range []image.Point{{showWide, showHigh}, {show8Wide, show8High}} {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, p.X, p.Y)))
		if w, room := r.width(r.tiny, widest), r.w-2*r.margin; w > room {
			t.Errorf("%v: the credit is %d wide, the page has %d", p, w, room)
		}
	}
}
