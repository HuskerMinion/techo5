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

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/remind"
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
		"clock":             {now: at, phase: "idle", weather: sky},
		"clock-sunny":       {now: at, phase: "idle", weather: home.Weather{Condition: "sunny", Temp: "88°"}},
		"clock-rainy":       {now: at, phase: "idle", weather: home.Weather{Condition: "rainy", Temp: "54°"}},
		"clock-night":       {now: at, phase: "idle", weather: home.Weather{Condition: "clear-night", Temp: "58°"}},
		"clock-no-weather":  {now: at, phase: "idle"},
		"clock-call-button": {now: at, phase: "idle", weather: sky, callButton: true},
		"drawer-call": {now: at, phase: "idle", weather: sky, showDrawer: true, drawerTab: drawerCall, demo: true,
			callees: []phone.Callee{{Name: "a", Device: true}, {Name: "b", Device: true}, {Name: "c", Device: true},
				{Name: "d", Number: "15551234567"}, {Name: "e", Number: "106"}}},
		"drawer-call-empty": {now: at, phase: "idle", weather: sky, showDrawer: true, drawerTab: drawerCall},
		"nowplaying": {now: at, phase: "idle", nowPlaying: true, playing: true, weather: sky,
			radio: home.Radio{Now: "KXYZ 101.1", Title: "Take It Easy", Artist: "Eagles"}},
		// Music Assistant's track, carried over Sendspin: named, with the three buttons.
		"nowplaying-music-assistant": {now: at, phase: "idle", nowPlaying: true, playing: true, weather: sky,
			radio: home.Radio{Playing: true, Now: "Music Assistant", Title: "Some Jazz", Artist: "The Quartet", Music: true}},
		"nowplaying-music-assistant-paused": {now: at, phase: "idle", nowPlaying: true, paused: true, weather: sky,
			radio: home.Radio{Now: "Music Assistant", Title: "Some Jazz", Artist: "The Quartet", Music: true}},
		"weather":         {now: at, phase: "idle", weather: sky, forecast: week, showWeather: true},
		"weather-rain":    {now: at, phase: "idle", weather: home.Weather{Condition: "rainy", Temp: "54°"}, forecast: week, showWeather: true, sky: fxRain},
		"weather-pouring": {now: at, phase: "idle", weather: home.Weather{Condition: "pouring", Temp: "54°"}, forecast: week, showWeather: true, sky: fxPour},
		"weather-snow":    {now: at, phase: "idle", weather: home.Weather{Condition: "snowy", Temp: "28°"}, forecast: week, showWeather: true, sky: fxSnow},
		"weather-storm":   {now: at, phase: "idle", weather: home.Weather{Condition: "lightning-rainy", Temp: "68°"}, forecast: week, showWeather: true, sky: fxStorm},
		"weather-fog":     {now: at, phase: "idle", weather: home.Weather{Condition: "fog", Temp: "48°"}, forecast: week, showWeather: true, sky: fxFog},
		"setup-ask":       {now: at, phase: "idle", weather: sky, setupAsking: true},
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
		"reminder": {now: at, phase: "idle", weather: sky, showReminder: true,
			reminder: remind.Reminder{Label: "Take the medication"}},
		"reminder-from-elsewhere": {now: at, phase: "idle", weather: sky, showReminder: true, reminderFrom: "Kitchen",
			reminder: remind.Reminder{Label: "Check the boiling eggs — they've been on for 10 minutes now"}},
		// Longer than three lines, to see the ellipsis rather than a sentence that stops.
		"reminder-longest": {now: at, phase: "idle", weather: sky, showReminder: true, reminderFrom: "Laundry Room",
			reminder: remind.Reminder{Label: "Move the washing to the dryer, then put the towels on the rack by the back door " +
				"so they are dry by the time everybody is back from the pool this afternoon"}},
		"reminder-and-announcement": {now: at, phase: "idle", weather: sky, showReminder: true,
			reminder:         remind.Reminder{Label: "Take the trash out"},
			showAnnouncement: true, announcement: announce.Message{From: "Kitchen", Text: "dinner is ready"}},
		"announce-drawer": {now: at, phase: "idle", weather: sky, showDrawer: true,
			drawerTab: drawerAnnounce, announceReady: true, announcePeers: 3},
		// The three pages that put two large answers at the foot of the screen. They are the only
		// thing on those pages somebody has to press, so how they look is worth a picture.
		"ringing-alarm": {now: at, phase: "idle",
			ring: ringState{alarm: &alarm.Ring{Label: "Wake up"}, snoozable: true}, snooze: 9},
		"ringing-timer": {now: at, phase: "idle", ring: ringState{timer: "Pasta"}},
		// Silenced by a button press and waiting to be told what that meant. It looks like a ring
		// that stopped and is not one, so the page has to say so.
		"ringing-silenced": {now: at, phase: "idle", snooze: 9,
			ring: ringState{alarm: &alarm.Ring{Label: "Wake up"}, snoozable: true, silenced: true}},
		"ringing-silenced-timer": {now: at, phase: "idle", snooze: 9,
			ring: ringState{timer: "Pasta", silenced: true}},
		// Muted, on the pages the header has to carry it onto. The ringing one is why the header
		// exists: an alarm sounding on a device that cannot hear "stop" used to look exactly like one
		// that could.
		"muted-clock": {now: at, phase: "idle", weather: sky, muted: true},
		"muted-ringing-alarm": {now: at, phase: "idle", muted: true,
			ring: ringState{alarm: &alarm.Ring{Label: "Wake up"}, snoozable: true}, snooze: 9},
		"muted-call": {now: at, phase: "idle", muted: true,
			call: phone.State{Phase: phone.Talking, Peer: "104", Since: at.Add(-90 * time.Second)}},
		// Muted while a browser is asking to be let in. The footer used to show one of these instead
		// of the other; now they are on different edges and neither hides the other.
		"muted-setup-ask": {now: at, phase: "idle", weather: sky, muted: true, setupAsking: true},
		"call-ringing":    {now: at, phase: "idle", call: phone.State{Phase: phone.Ringing, Peer: "104"}},
		// A timer finishing during a call. This page is drawn over the ringing one, so without a
		// line here the noise has nothing on screen to explain it, and the buttons below belong to
		// the call.
		"call-with-timer-ringing": {now: at, phase: "idle", snooze: 9,
			call: phone.State{Phase: phone.Talking, Peer: "104", Since: at.Add(-90 * time.Second)},
			ring: ringState{timer: "Pasta"}},
		"call-talking": {now: at, phase: "idle", call: phone.State{Phase: phone.Talking, Peer: "104", Since: at.Add(-90 * time.Second)}},
		"settings-sound": {now: at, phase: "idle", showSheet: true,
			sheet: settings{cat: catSound, volume: 15, wakeWord: "Okay Nabu"}},
		"settings-sound-tone": {now: at, phase: "idle", showSheet: true,
			sheet: settings{cat: catSound, volume: 15, wakeWord: "Okay Nabu", cardScroll: 210}},
		"clock-music-strip": {now: at, phase: "idle", weather: sky, strip: true, playing: true,
			radio: home.Radio{Now: "Music Assistant", Title: "Reason That I Sing", Artist: "Release", Music: true}},
		"clock-music-strip-paused": {now: at, phase: "idle", weather: sky, strip: true, paused: true,
			radio: home.Radio{Now: "Music Assistant", Title: "A Very Long Song Title That Has To Be Cut Short", Artist: "Somebody", Music: true}},
		"nowplaying-faved": {now: at, phase: "idle", nowPlaying: true, playing: true, faved: true, weather: sky,
			radio: home.Radio{Now: "Music Assistant", Title: "Some Jazz", Artist: "The Quartet", Music: true}},
		"clock-missed": {now: at, phase: "idle", missed: "Missed: timer \"Pasta\" at 2:03 PM yesterday · and 1 more"},
		"settings-alarms": {now: at, phase: "idle", showSheet: true, snooze: 9,
			sheet: settings{cat: catAlarms}},
		"settings-alarms-end": {now: at, phase: "idle", showSheet: true, snooze: 9,
			sheet: settings{cat: catAlarms, cardScroll: 1000}},
		// The editor with the alarm's own wake light, scrolled to it.
		"settings-alarm-editor": {now: at, phase: "idle", showSheet: true, snooze: 9,
			sheet: settings{cat: catAlarms, cardScroll: 1000}, draft: &alarmDraft{
				alarm: config.Alarm{ID: "a", Hour: 6, Minute: 45, Days: config.DaysWeekdays, Label: "Wake up", Sunrise: 10, On: true}}},
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
