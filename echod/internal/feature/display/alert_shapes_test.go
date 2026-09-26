//go:build !dot

package display

import (
	"image"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// "+N" counts kinds, as the pills group them: two Wind Advisories and a Flood Watch are one more.
func TestOtherKinds(t *testing.T) {
	wind, flood := home.Alert{Event: "Wind Advisory"}, home.Alert{Event: "Flood Watch"}
	for _, tc := range []struct {
		here []home.Alert
		want int
	}{{nil, 0}, {[]home.Alert{wind}, 0}, {[]home.Alert{wind, wind}, 0}, {[]home.Alert{wind, wind, flood}, 1}} {
		if got := otherKinds(tc.here); got != tc.want {
			t.Errorf("%d alerts: +%d, want +%d", len(tc.here), got, tc.want)
		}
	}
}

// The overlay is let go once the alerts end, rather than kept the page's size for nothing.
func TestAlertOverlayLetGo(t *testing.T) {
	dst := image.NewRGBA(image.Rect(0, 0, 64, 64))
	ring := [][2]float64{{-96.1, 41.1}, {-95.9, 41.1}, {-95.9, 41.3}, {-96.1, 41.3}}
	var cache alertOverlay
	alertShapes(dst, home.RadarView{}, []home.Alert{{ID: "a", Rings: [][][2]float64{ring}}}, image.Point{}, 1, nil, &cache)
	if cache.img == nil {
		t.Fatal("no overlay kept while there are alerts")
	}
	alertShapes(dst, home.RadarView{}, nil, image.Point{}, 1, nil, &cache)
	if cache.img != nil || cache.key != "" {
		t.Error("the overlay was kept after the alerts ended")
	}
}
