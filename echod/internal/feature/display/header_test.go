//go:build !dot && !spot

package display

import (
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
)

// headerStrip draws a scene and returns the top strip, which is the only part of the panel the
// header is allowed to touch.
func headerStrip(s scene, wide, high int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, wide, high))
	r := newRenderer(img)
	r.draw(s)

	strip := make([]byte, 0, wide*r.s(headerH)*4)
	for y := range r.s(headerH) {
		for x := range wide {
			p := img.RGBAAt(x, y)
			strip = append(strip, p.R, p.G, p.B, p.A)
		}
	}
	return strip
}

func same(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func showPanels() []struct {
	name       string
	wide, high int
} {
	return []struct {
		name       string
		wide, high int
	}{
		{"show5", showWide, showHigh},
		{"show8", show8Wide, show8High},
	}
}

// The microphone line is drawn on every page, not only the two the footer reaches.
//
// It used to live in the footer, and footer() is called from the weather page and the idle path and
// nowhere else. So a muted device said nothing at all while ringing, on a call, pairing, in settings,
// on the camera or in setup — and the ringing page is the one where it matters most, because an
// alarm that cannot hear "stop" looks exactly like one that can.
func TestTheMicrophoneLineIsOnEveryPage(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	sky := home.Weather{Condition: "partlycloudy", Temp: "72°"}

	pages := map[string]scene{
		"clock":   {now: at, phase: "idle", weather: sky},
		"weather": {now: at, phase: "idle", weather: sky, showWeather: true},
		"ringing-alarm": {now: at, phase: "idle",
			ring: ringState{alarm: &alarm.Ring{Label: "Wake up"}, snoozable: true}, snooze: 9},
		"ringing-timer": {now: at, phase: "idle", ring: ringState{timer: "Pasta"}},
		"call":          {now: at, phase: "idle", call: phone.State{Phase: phone.Talking, Peer: "104", Since: at}},
		"setup-ask":     {now: at, phase: "idle", weather: sky, setupAsking: true},
		"listening":     {now: at, phase: "listening", since: at, weather: sky},
		"sunrise":       {now: at, phase: "idle", sunrise: 0.6, sunriseFace: true},
	}

	for _, panel := range showPanels() {
		for name, s := range pages {
			quiet := headerStrip(s, panel.wide, panel.high)

			s.muted = true
			if same(quiet, headerStrip(s, panel.wide, panel.high)) {
				t.Errorf("%s/%s: the microphone is cut and the top of the screen is unchanged",
					panel.name, name)
			}
		}
	}
}

// The settings sheet is the exception, on purpose. It has a Microphone row that reads "Muted" or
// "Listening" with the switch beside it, and its card reaches the top edge — so a line over it would
// say the same thing worse and cut the card's top off.
func TestTheSettingsSheetKeepsItsOwnTop(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	sheet := scene{now: at, phase: "idle", showSheet: true,
		sheet: settings{cat: catSound, volume: 15, wakeWord: "Okay Nabu"}}

	for _, panel := range showPanels() {
		quiet := headerStrip(sheet, panel.wide, panel.high)

		muted := sheet
		muted.muted = true
		if !same(quiet, headerStrip(muted, panel.wide, panel.high)) {
			t.Errorf("%s: the header drew over the settings sheet", panel.name)
		}
	}
}

// The header goes over the page rather than under it, so a page that fills the panel to the top edge
// cannot bury it.
func TestTheHeaderIsDrawnOverAFullScreenPage(t *testing.T) {
	at := time.Date(2026, 9, 16, 6, 40, 0, 0, time.Local)
	sunrise := scene{now: at, phase: "idle", sunrise: 0.6, sunriseFace: true, muted: true}

	img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
	r := newRenderer(img)
	r.draw(sunrise)

	// The words are drawn in amber on a pill of background, so both have to be there: if the sunrise
	// were still on top, neither would be.
	var bg, ink int
	for y := range r.s(headerH) {
		for x := range showWide {
			switch img.RGBAAt(x, y) {
			case walnut:
				bg++
			case amber:
				ink++
			}
		}
	}
	if bg == 0 {
		t.Error("no pill behind the words; the sunrise is still under them")
	}
	if ink == 0 {
		t.Error("the header drew no words over the sunrise")
	}
}
