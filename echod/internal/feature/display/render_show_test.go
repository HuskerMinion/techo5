//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The panel's own size, so a preview is what the device draws rather than something like it.
const (
	showWide = 960
	showHigh = 480

	// The Echo Show 8's panel, which draws the same layout scaled.
	show8Wide = 1280
	show8High = 800
)

// Every scene draws without panicking; with SHOW_PREVIEW set to a directory, each is written there
// as a PNG to look at. The Spot has had this since its screen was built (render_spot_test.go); the
// Show never did, so a change to its layout was argued about in words.
func TestShowScenesDraw(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	sky := home.Weather{Condition: "partlycloudy", Temp: "72°"}
	var week []hass.Day
	for i, c := range []string{"partlycloudy", "rainy", "lightning-rainy", "sunny", "snowy", "cloudy"} {
		week = append(week, hass.Day{When: at.AddDate(0, 0, i), Condition: c, High: float64(78 - 3*i), Low: float64(55 - 2*i), Rain: 10 * i})
	}

	scenes := map[string]scene{
		"clock":            {now: at, phase: "idle", weather: sky},
		"clock-sunny":      {now: at, phase: "idle", weather: home.Weather{Condition: "sunny", Temp: "88°"}},
		"clock-rainy":      {now: at, phase: "idle", weather: home.Weather{Condition: "rainy", Temp: "54°"}},
		"clock-night":      {now: at, phase: "idle", weather: home.Weather{Condition: "clear-night", Temp: "58°"}},
		"clock-no-weather": {now: at, phase: "idle"},
		"nowplaying": {now: at, phase: "idle", nowPlaying: true, playing: true, weather: sky,
			radio: home.Radio{Now: "KXYZ 101.1", Title: "Take It Easy", Artist: "Eagles"}},
		"weather":   {now: at, phase: "idle", weather: sky, forecast: week, showWeather: true},
		"setup-ask": {now: at, phase: "idle", weather: sky, setupAsking: true},
		"announcement": {now: at, phase: "idle", weather: sky, showAnnouncement: true,
			announcement: announce.Message{From: "Kitchen", Text: "dinner is ready"}},
		"announcement-voice": {now: at, phase: "idle", weather: sky, showAnnouncement: true,
			announcement: announce.Message{From: "Laundry Room"}},
		// A long room name with words as well. The words used to start at a fixed offset from the
		// edge, so a name wider than that was drawn straight through by them.
		"announcement-long": {now: at, phase: "idle", weather: sky, showAnnouncement: true,
			announcement: announce.Message{From: "Laundry Room", Text: "the washing is finished"}},
		// As long as an automation is allowed to send, to see where it stops.
		"announcement-longest": {now: at, phase: "idle", weather: sky, showAnnouncement: true,
			announcement: announce.Message{From: "Laundry Room",
				Text: "the washing machine has finished its cycle and the door is unlocked now"}},
		"announce-recording": {now: at, phase: "idle", weather: sky, announceRecording: true, announcePeers: 3},
		"announce-drawer": {now: at, phase: "idle", weather: sky, showDrawer: true,
			drawerTab: drawerAnnounce, announceReady: true, announcePeers: 3},
		// The three pages that put two large answers at the foot of the screen. They are the only
		// thing on those pages somebody has to press, so how they look is worth a picture.
		"ringing-alarm": {now: at, phase: "idle",
			ring: ringState{alarm: &alarm.Ring{Label: "Wake up"}, snoozable: true}, snooze: 9},
		"ringing-timer": {now: at, phase: "idle", ring: ringState{timer: "Pasta"}},
		"call-ringing":  {now: at, phase: "idle", call: phone.State{Phase: phone.Ringing, Peer: "104"}},
		"call-talking":  {now: at, phase: "idle", call: phone.State{Phase: phone.Talking, Peer: "104", Since: at.Add(-90 * time.Second)}},
		"settings-sound": {now: at, phase: "idle", showSheet: true,
			sheet: settings{cat: catSound, volume: 15, wakeWord: "Okay Nabu"}},
		"settings-sound-tone": {now: at, phase: "idle", showSheet: true,
			sheet: settings{cat: catSound, volume: 15, wakeWord: "Okay Nabu", cardScroll: 210}},
	}

	// The light before an alarm, frame by frame: the same curve the panel follows, with the sun's
	// face on, for looking at away from a device at six in the morning.
	wake := at.Add(20 * time.Minute)
	for i := 0; i <= 20; i++ {
		p := float64(i) / 20
		scenes[fmt.Sprintf("sunrise-%02d", i)] = scene{
			now: wake.Add(-time.Duration((1-p)*20) * time.Minute), phase: "idle",
			sunrise: p, sunriseFace: true,
		}
	}

	dir := os.Getenv("SHOW_PREVIEW")
	// Both panels this build draws on. The Show 8 is not a second layout: it is this one scaled, and
	// the point of drawing it here is that the scaling can be looked at without a device.
	for _, panel := range []struct {
		name       string
		wide, high int
	}{
		{"", showWide, showHigh},
		{"-show8", show8Wide, show8High},
	} {
		for name, s := range scenes {
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			newRenderer(img).draw(s)
			if dir == "" {
				continue
			}
			f, err := os.Create(filepath.Join(dir, name+panel.name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, img); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// A size written as a literal in this package is in the Show 5's pixels, and the Show 8 scales them.
// These are the two ends of that: 4:3 across, so 40 becomes 53 and the clock's 230 becomes 307.
func TestFixedSizesScaleToTheShow8(t *testing.T) {
	show5 := &paint{w: showWide, h: showHigh, sNum: showWide, sDen: drawnFor}
	show8 := &paint{w: show8Wide, h: show8High, sNum: show8Wide, sDen: drawnFor}

	if show5.scaled() {
		t.Error("the panel the layout was drawn for reports itself as scaled")
	}
	if !show8.scaled() {
		t.Error("the Show 8 reports itself as unscaled")
	}
	for _, c := range []struct{ in, want int }{
		{0, 0}, {1, 1}, {3, 4}, {40, 53}, {230, 307}, {-40, -53},
	} {
		if got := show5.s(c.in); got != c.in {
			t.Errorf("a Show 5 scaled %d to %d; it must not scale at all", c.in, got)
		}
		if got := show8.s(c.in); got != c.want {
			t.Errorf("a Show 8 scaled %d to %d, want %d", c.in, got, c.want)
		}
	}
}
