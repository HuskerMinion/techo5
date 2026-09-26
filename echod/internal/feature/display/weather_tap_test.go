//go:build !dot && !spot

package display

import (
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// A tap on the home screen's weather is known for one only while the weather is drawn: on the clock
// with a reading, not on a page without it.
func TestWeatherTapFollowsTheDrawing(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, 960, 480)))
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.Local)
	r.draw(scene{now: now, phase: "idle", weather: home.Weather{Temp: "72°", Condition: "partlycloudy"}})
	at := image.Pt(r.margin+10, r.margin+15)
	if !r.weatherTapped(at) {
		t.Fatalf("a tap on the weather at %v was missed (weather at %v)", at, r.weatherAt)
	}
	if r.weatherTapped(image.Pt(r.w/2, r.h/2)) {
		t.Error("a tap on the clock counted as the weather")
	}
	r.draw(scene{now: now, phase: "idle"}) // no reading: no weather drawn
	if r.weatherTapped(at) {
		t.Error("a tap where the weather used to be counted")
	}
}
