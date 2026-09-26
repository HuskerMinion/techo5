//go:build spot

package display

import (
	"fmt"
	"image"
	"image/png"
	"math"
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
	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The dials: each item's rest puts it at the top, a tap lands on the item under it or the middle, and
// snapping always takes the short way round.
func TestDialGeometry(t *testing.T) {
	for _, items := range [][]menuItem{mainItems, cameraItems(make([]config.Camera, 5))} {
		n := len(items)
		for i := range items {
			rot := restFor(i, n)
			if got := topItem(rot, n); got != i {
				t.Errorf("n=%d rest for %d: top is %d", n, i, got)
			}
			x, y := itemPos(i, n, rot)
			if math.Abs(x-center) > 0.5 || math.Abs(y-(center-dialR)) > 0.5 {
				t.Errorf("n=%d item %d at rest is at %.1f,%.1f, not the top", n, i, x, y)
			}
			for j := range items {
				jx, jy := itemPos(j, n, rot)
				if got, middle := dialHitAt(int(math.Round(jx)), int(math.Round(jy)), rot, n); middle || got != j {
					t.Errorf("n=%d rot for %d: tap on item %d hit %d (middle %v)", n, i, j, got, middle)
				}
			}
		}
		if d := nearestRest(restFor(0, n), n-1, n) - restFor(0, n); math.Abs(d) > math.Pi {
			t.Errorf("n=%d snapping from 0 to %d turns %.2f rad, the long way", n, n-1, d)
		}
	}
	if _, middle := dialHitAt(center, center, 0, len(mainItems)); !middle {
		t.Error("the center is not the middle")
	}
}

// testPicture is a 640x480 gradient with a bright square, to see the crop and the mirror.
func testPicture() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	for y := 0; y < 480; y++ {
		for x := 0; x < 640; x++ {
			j := (y*640 + x) * 4
			img.Pix[j], img.Pix[j+1], img.Pix[j+2], img.Pix[j+3] = uint8(x*255/639), uint8(y*255/479), 120, 255
			if x > 380 && x < 460 && y > 200 && y < 280 {
				img.Pix[j], img.Pix[j+1], img.Pix[j+2] = 255, 255, 255
			}
		}
	}
	return img
}

// Every scene draws without panicking; with SPOT_PREVIEW set to a directory, each is written there as
// a PNG to look at.
func TestRoundScenesDraw(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	sky := home.Weather{Condition: "partlycloudy", Temp: "72°"}
	var week []hass.Day
	for i, c := range []string{"partlycloudy", "rainy", "lightning-rainy", "sunny", "snowy", "cloudy"} {
		week = append(week, hass.Day{When: at.AddDate(0, 0, i), Condition: c, High: float64(78 - 3*i), Low: float64(55 - 2*i), Rain: 10 * i})
	}
	scenes := map[string]roundScene{
		"clock-weather":     {now: at, phase: "idle", weather: sky, timers: []timer.Countdown{{Left: 272 * time.Second, Total: 600 * time.Second, Active: true}}},
		"menu-weather":      {now: at, phase: "idle", weather: sky, menuOpen: true, menuMode: modeMain, menuSel: 4, menuRot: restFor(4, len(mainItems))},
		"weather":           {now: at, phase: "idle", weather: sky, forecast: week, menuOpen: true, menuMode: modeWeather},
		"weather-now":       {now: at, phase: "idle", weather: home.Weather{Condition: "clear-night", Temp: "58°"}, menuOpen: true, menuMode: modeWeather},
		"weather-none":      {now: at, phase: "idle", menuOpen: true, menuMode: modeWeather},
		"nowplaying":        {now: at, phase: "idle", nowPlaying: true, playing: true, weather: sky, radio: home.Radio{Now: "KXYZ 101.1", Title: "Take It Easy", Artist: "Eagles", Art: testPicture(), Thumb: testPicture()}},
		"nowplaying-logo":   {now: at, phase: "idle", nowPlaying: true, playing: true, radio: home.Radio{Now: "Morning News 850", Logo: true, Thumb: testPicture()}},
		"cameras":           {now: at, phase: "idle", showCamera: true, camera: home.CameraView{Entity: "camera.deck", Name: "Deck", Frame: testPicture()}, menuOpen: true, menuMode: modeCameras, menuSel: 1, menuRot: restFor(1, 3), cameras: []config.Camera{{Entity: home.LocalCamera, Name: "This Spot"}, {Entity: "camera.deck", Name: "Deck"}, {Entity: "camera.front_door", Name: "Front door"}}},
		"contacts":          {now: at, phase: "idle", menuOpen: true, menuMode: modeContacts, phoneReady: true, houseReady: true, contacts: []phone.Callee{{Name: "Kitchen", Device: true}, {Name: "Garage", Device: true}, {Name: "Alex", Number: "15551234567"}, {Name: "Sam", Number: "15557654321"}, {Name: "Laundry Room", Number: "101"}, {Name: "Bathroom", Number: "102"}, {Name: "Office", Number: "104"}}, contactTop: 1},
		"clock-call-button": {now: at, phase: "idle", weather: home.Weather{Condition: "partlycloudy", Temp: "72°"}, callButton: true},
		"cameras-many":      {now: at, phase: "idle", showCamera: true, camera: home.CameraView{Entity: "camera.c3", Name: "Deck"}, menuOpen: true, menuMode: modeCameras, menuSel: 3, menuRot: restFor(3, 9), cameras: []config.Camera{{Entity: "local", Name: "This Spot"}, {Entity: "camera.c1", Name: "Front door"}, {Entity: "camera.c2", Name: "Garage side"}, {Entity: "camera.c3", Name: "Deck"}, {Entity: "camera.c4", Name: "Dining room"}, {Entity: "camera.c5", Name: "Garage inside"}, {Entity: "camera.c6", Name: "Shed front"}, {Entity: "camera.c7", Name: "Shed back"}, {Entity: "camera.c8", Name: "Garage front"}}},
		"menu-call":         {now: at, phase: "idle", menuOpen: true, menuMode: modeMain, menuSel: 1, menuRot: restFor(1, len(mainItems)), phoneReady: true, contactCount: 4},
		"ringing-alarm":     {now: at, phase: "idle", ringing: ringing{alarm: &alarm.Ring{Label: "Wake up", At: at}, snoozeIn: 9}},
		"ringing-timer":     {now: at, phase: "idle", ringing: ringing{timer: "pasta", timerOn: true}},
		// Quieted by a button press and waiting to be told what that meant. It wears the ringing
		// face while making no sound, so the face has to say which of the two it is.
		"ringing-silenced": {now: at, phase: "idle",
			ringing: ringing{alarm: &alarm.Ring{Label: "Wake up", At: at}, snoozeIn: 9, silenced: true}},
		"ringing-silenced-timer": {now: at, phase: "idle",
			ringing: ringing{timer: "pasta", timerOn: true, silenced: true}},
		"nowplaying-paused": {now: at, phase: "idle", nowPlaying: true, paused: true, radio: home.Radio{Chosen: "Morning News"}},
		"nowplaying-music-assistant": {now: at, phase: "idle", nowPlaying: true, playing: true,
			radio: home.Radio{Playing: true, Now: "Music Assistant", Title: "Some Jazz", Artist: "The Quartet", Music: true}},
		"radio-list":    {now: at, phase: "idle", playing: true, menuOpen: true, menuMode: modeRadio, radioSel: 2, radio: home.Radio{Configured: true, Source: "local", Sources: 2, Now: "KXYZ 101.1", Stations: []string{"KAAA 90.1", "KXYZ 101.1", "The Mountain 99.5 Classic Rock and More", "KBBB 104.3"}}},
		"radar-loading": {now: at, phase: "idle", menuOpen: true, menuMode: modeWeather, radarOn: true, radar: home.RadarView{Loading: true}},
		"radar":         {now: at, phase: "idle", menuOpen: true, menuMode: modeWeather, radarOn: true, radar: home.RadarView{Frames: []home.RadarFrame{{Image: testPicture(), At: at}}}},
		"camera-none":   {now: at, phase: "idle", showCamera: true, camera: home.CameraView{Entity: "camera.front_door", Name: "Front door"}},
		"camera":        {now: at, phase: "idle", showCamera: true, cameraLive: true, camera: home.CameraView{Entity: home.LocalCamera, Name: "This Spot", Frame: testPicture()}},
		"menu-camera":   {now: at, phase: "idle", cameraLive: true, menuOpen: true, menuMode: modeMain, menuSel: 5, menuRot: restFor(5, len(mainItems))},
		"bt-pairing":    {now: at, phase: "idle", btPairing: true, weather: sky},
		"clock":         {now: at, phase: "idle", volume: 12, maxVolume: 30},
		"clock-timer":   {now: at, phase: "idle", timers: []timer.Countdown{{Name: "pasta", Left: 4*time.Minute + 32*time.Second, Total: 10 * time.Minute, Active: true}}},
		"muted":         {now: at, phase: "idle", muted: true},
		"listening":     {now: at, phase: "listening"},
		"thinking":      {now: at, phase: "thinking", heard: "what's the weather going to be like this afternoon"},
		"replying":      {now: at, phase: "replying", heard: "what time is it", reply: "It's 2:07 PM. Have a great afternoon, and don't forget the pasta timer is still running in the kitchen."},
		"volume":        {now: at, phase: "idle", volume: 18, maxVolume: 30, showVolume: true},
		"menu":          {now: at, phase: "idle", volume: 12, menuOpen: true, menuMode: modeMain, menuSel: 0, menuRot: restFor(0, len(mainItems))},
		"menu-timers":   {now: at, phase: "idle", menuOpen: true, menuMode: modeMain, menuSel: 6, menuRot: restFor(6, len(mainItems)) + 0.3, timers: []timer.Countdown{{Left: 272 * time.Second, Total: 600 * time.Second, Active: true}}},
		"jog-volume":    {now: at, phase: "idle", menuOpen: true, menuMode: modeVolume, volume: 14, maxVolume: 30},
		"setup-ask":     {now: at, phase: "idle", setupAsking: true},
		"announcement":  {now: at, phase: "idle", showAnnouncement: true, announcement: announce.Message{From: "Guest's Desk", Text: "dinner is ready, come down"}},
		"announcement-voice": {now: at, phase: "idle", showAnnouncement: true,
			announcement: announce.Message{From: "Laundry Room"}},
		"reminder": {now: at, phase: "idle", showReminder: true,
			reminder: remind.Reminder{Label: "Take the trash out"}},
		// Longer than the box: the words scroll rather than stopping after two lines, at rest and
		// dragged to the end.
		"reminder-long": {now: at, phase: "idle", showReminder: true, reminderFrom: "Kitchen",
			reminder: remind.Reminder{Label: "Check the boiling eggs — they've been on for 10 minutes now, take them off before they crack"}},
		"reminder-long-end": {now: at, phase: "idle", showReminder: true, reminderFrom: "Kitchen", reminderScroll: 1 << 20,
			reminder: remind.Reminder{Label: "Check the boiling eggs — they've been on for 10 minutes now, take them off before they crack"}},
		"announce-recording": {now: at, phase: "idle", announceRecording: true, announcePeers: 3},
		// Muted while an announcement has the face: the one place the state was invisible, and the
		// one time somebody is reaching for the button.
		"announcement-muted": {now: at, phase: "idle", muted: true, showAnnouncement: true,
			announcement: announce.Message{From: "Laundry Room"}},
		"menu-announce": {now: at, phase: "idle", menuOpen: true, menuMode: modeMain,
			menuSel: 8, menuRot: restFor(8, len(mainItems)), announceReady: true, announcePeers: 3},
		"settings-general":  spotScene(catGeneral),
		"settings-tzpick":   spotPicker(catGeneral, "timezone"),
		"settings-tzcommon": spotPicker(catGeneral, "timezone:Common"),
		"settings-sound":    spotScene(catSound),
		"settings-privacy":  spotScene(catSecurity),
		"clock-missed":      {now: at, phase: "idle", missed: "Missed: timer \"Pasta\" at 2:03 PM"},
		"settings-alarms":   spotScene(catAlarms),
		"settings-alarms-end": func() roundScene {
			s := spotScene(catAlarms)
			s.sheet.st.cardScroll = 1000
			return s
		}(),
	}
	// Weather alerts: the clock's pill, the alert face (a second one, scrolled), the rain map's pill.
	wind := home.Alert{ID: "a", Event: "Wind Advisory", Severity: "Moderate", Sender: "NWS Omaha/Valley NE",
		Ends: at.Add(20 * time.Hour), Color: home.AlertColor("Wind Advisory", ""), Here: true,
		Description: "What: North winds 15 to 25 mph with gusts up to 45 mph.\nWhere: Douglas, Sarpy and Cass counties.",
		Instruction: "Secure outdoor objects."}
	storm := home.Alert{ID: "b", Event: "Severe Thunderstorm Warning", Severity: "Severe", Ends: at.Add(40 * time.Minute),
		Color: home.AlertColor("Severe Thunderstorm Warning", ""), Here: true, Storm: true,
		Description: "At 2:05 PM, a severe thunderstorm was located near Elkhorn, moving east at 30 mph.", Instruction: "Move to an interior room."}
	alerts := home.AlertView{Here: []home.Alert{storm, wind}, Near: []home.Alert{storm, wind}}
	scenes["clock-alert"] = roundScene{now: at, phase: "idle", weather: sky, alerts: alerts}
	scenes["alert-face"] = roundScene{now: at, phase: "idle", showAlert: true, alerts: alerts}
	scenes["alert-face-2"] = roundScene{now: at, phase: "idle", showAlert: true, alertIdx: 1, alertScroll: 2, alerts: alerts}
	scenes["radar-alert"] = roundScene{now: at, phase: "idle", menuOpen: true, menuMode: modeWeather, radarOn: true, alerts: alerts,
		radar: home.RadarView{Frames: []home.RadarFrame{{Image: testPicture(), At: at}}}}

	// The light before an alarm on the round face, frame by frame.
	wake := at.Add(20 * time.Minute)
	for i := 0; i <= 20; i++ {
		p := float64(i) / 20
		scenes[fmt.Sprintf("sunrise-%02d", i)] = roundScene{
			now: wake.Add(-time.Duration((1-p)*20) * time.Minute), phase: "idle",
			sunrise: p, sunriseFace: true,
		}
	}

	dir := os.Getenv("SPOT_PREVIEW")
	for name, s := range scenes {
		img := image.NewRGBA(image.Rect(0, 0, side, side))
		newRoundRenderer(img).draw(s)
		if dir == "" {
			continue
		}
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}

// spotPicker is a settings card with one row's list of choices open.
func spotPicker(cat category, row string) roundScene {
	sc := spotScene(cat)
	sc.sheet.st.picker = row
	return sc
}
