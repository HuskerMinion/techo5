//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
	"github.com/HuskerMinion/techo5/echod/internal/feature/remind"
	"github.com/HuskerMinion/techo5/echod/internal/feature/talkback"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// noticeDisplay is a Display with a lit panel, a light to turn on and off, and a notifier that keeps
// quiet (it thinks it is night), so a test can put a notification up without a chime.
func noticeDisplay(t *testing.T, w, h int) *Display {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Screen().Welcomed(true); err != nil {
		t.Fatal(err)
	}
	notify.SetNight(func(time.Time) bool { return true })
	t.Cleanup(func() {
		notify.Get().Dismiss()
		remind.Get().Stop()
		notify.SetNight(func(time.Time) bool { return false })
	})
	return &Display{
		r: newRenderer(image.NewRGBA(image.Rect(0, 0, w, h))), on: true, poke: make(chan struct{}, 1),
		view: voice.State{Phase: "idle"},
		light: &esphome.Light{
			Base:                esphome.Base{ObjectID: "screen", Name: "Screen", Icon: "mdi:monitor"},
			SupportedColorModes: []esphome.ColorMode{esphome.ColorModeBrightness},
		},
	}
}

var (
	noticeChanged  = make(chan struct{}, 64)
	noticeListened sync.Once
)

// listenForNotice is how a test knows a tap's dismissal is finished: Changed is emitted after the
// event is reported, and a tap dismisses on its own goroutine, which reads the config the next test
// replaces. Waiting on it is what orders the two.
func listenForNotice() {
	noticeListened.Do(func() {
		notify.Get().Changed.Listen(func(struct{}) {
			select {
			case noticeChanged <- struct{}{}:
			default:
			}
		})
	})
	for len(noticeChanged) > 0 {
		<-noticeChanged
	}
}

// dismissedByTap waits for the dismissal a tap started to finish.
func dismissedByTap(t *testing.T, what string) {
	t.Helper()
	select {
	case <-noticeChanged:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not happen", what)
	}
	if noticeUp() {
		t.Errorf("%s left the notification up", what)
	}
}

// within waits for what a tap does on its own goroutine.
func within(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("%s did not happen", what)
}

func tapAt(d *Display, p image.Point) { d.gesture(touch.Gesture{Kind: touch.Tap, X: p.X, Y: p.Y}) }

func noticeUp() bool { _, up := notify.Get().Showing(); return up }

// The taps, through gesture() where their order is decided: a tap on the card puts a notification away,
// a tap elsewhere is not a tap on it, and a reminder over it takes the tap first.
func TestATapOnANotificationCard(t *testing.T) {
	d := noticeDisplay(t, showWide, showHigh)
	for _, panel := range showPanels() {
		d.r = newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
		mid := image.Pt(panel.wide/2, panel.high/2)

		if err := notify.Get().Notify("The washer is done", "Washer", "", "", "", "", 60); err != nil {
			t.Fatal(err)
		}
		tapAt(d, image.Pt(4, 4))
		time.Sleep(50 * time.Millisecond)
		if !noticeUp() {
			t.Errorf("%s: a tap off the card put the notification away", panel.name)
		}
		listenForNotice()
		tapAt(d, mid)
		dismissedByTap(t, panel.name+": a tap on the card")
		time.Sleep(100 * time.Millisecond)

		// A reminder over a notification: its tap comes first, and the notification stays.
		if err := notify.Get().Notify("Under the reminder", "", "", "", "", "", 60); err != nil {
			t.Fatal(err)
		}
		remind.Get().Fire("Take the trash out", nil)
		if _, up := remind.Get().Showing(); !up {
			t.Fatalf("%s: the reminder did not come up", panel.name)
		}
		tapAt(d, mid)
		within(t, panel.name+": the reminder being put away", func() bool { _, up := remind.Get().Showing(); return !up })
		if !noticeUp() {
			t.Errorf("%s: the tap that put the reminder away dismissed the notification under it", panel.name)
		}
		listenForNotice()
		tapAt(d, mid)
		dismissedByTap(t, panel.name+": the tap that follows the reminder")
	}
}

// A dark panel is lit for a notification once, by day: a screen turned off while it is up stays off, as
// a video's does, and at night nothing lights it.
func TestANotificationLightsADarkPanelOnce(t *testing.T) {
	d := noticeDisplay(t, showWide, showHigh)
	d.on = false
	if err := notify.Get().Notify("The washer is done", "", "", "", "", "", 60); err != nil {
		t.Fatal(err)
	}

	if d.noticeLights(false, true) != false {
		t.Fatal("a panel was lit for a notification at night")
	}
	if !d.noticeLights(false, false) || !d.on {
		t.Fatal("a notification by day did not light a dark panel")
	}
	d.on = false // turned off from Home Assistant, or by an empty room
	if d.noticeLights(false, false) || d.on {
		t.Error("a panel turned off with the notification still up was lit again")
	}

	if err := notify.Get().Notify("The dryer is done", "", "", "", "", "", 60); err != nil {
		t.Fatal(err)
	}
	if !d.noticeLights(false, false) || !d.on {
		t.Error("the next notification did not light the panel")
	}
}

func atNight(t *testing.T) {
	t.Helper()
	turned(t, "on", time.Now())
	if !nightNow(time.Now()) {
		t.Fatal("night is not on")
	}
}

// nightScreen is the frame the display would build at night on the night light's red clock, with the
// notification (if any) as the gate leaves it, drawn to pixels.
func nightScreen(t *testing.T, d *Display, at time.Time) (scene, *image.RGBA) {
	t.Helper()
	s := scene{now: at, phase: "idle", redClock: d.nightGlow && config.Get().Screen.NightRed}
	s.notification, s.showNotification = notify.Get().Card()
	d.gateNotice(&s, nightNow(at))
	s.camera, s.showCamera = home.Get().Camera()
	s.camera, s.showCamera = d.gateCamera(s.camera, s.showCamera, nightNow(at))
	img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
	newRenderer(img).draw(s)
	return s, img
}

func samePixels(a, b *image.RGBA) bool { return string(a.Pix) == string(b.Pix) }

// At night a notification waits: the night clock stays, and the first touch shows the card. A tap on the
// card then puts it away, and the red clock comes back.
func TestANotificationWaitsOutTheNightForATouch(t *testing.T) {
	d := noticeDisplay(t, showWide, showHigh)
	atNight(t)
	if err := config.Set().Screen().AtNight(true, true); err != nil {
		t.Fatal(err)
	}
	d.nightGlow = true
	at := time.Now()
	_, redOnly := nightScreen(t, d, at)

	if err := notify.Get().Notify("The washer is done", "Washer", "", "", "", "", 600); err != nil {
		t.Fatal(err)
	}
	s, held := nightScreen(t, d, at)
	if s.showNotification || !s.redClock {
		t.Fatalf("a fresh notification at night: showNotification=%v redClock=%v, want held behind the red clock", s.showNotification, s.redClock)
	}
	if !samePixels(held, redOnly) {
		t.Fatal("the held notification changed what the night screen draws")
	}

	tapAt(d, image.Pt(5, 5)) // the first touch, off the card's place
	s, shown := nightScreen(t, d, at)
	if !s.showNotification || s.redClock {
		t.Fatalf("after a touch: showNotification=%v redClock=%v, want the card and no red clock", s.showNotification, s.redClock)
	}
	if samePixels(shown, redOnly) {
		t.Fatal("the released card is not drawn")
	}

	// Tapped away on the night light, the card goes and the red clock is drawn again.
	listenForNotice()
	tapAt(d, image.Pt(showWide/2, showHigh/2))
	dismissedByTap(t, "a tap on the card on the night light")
	if _, after := nightScreen(t, d, at); !samePixels(after, redOnly) {
		t.Error("the red clock did not come back after the card was tapped away")
	}

	// The next one is held again, and a notification that came by day is not.
	if err := notify.Get().Notify("The dryer is done", "", "", "", "", "", 600); err != nil {
		t.Fatal(err)
	}
	if s, _ = nightScreen(t, d, at); s.showNotification {
		t.Error("a second notification at night was drawn without a touch")
	}
	card, up := notify.Get().Card()
	s = scene{now: time.Now(), phase: "idle", notification: card, showNotification: up}
	d.gateNotice(&s, false)
	if !s.showNotification {
		t.Error("a notification by day was held")
	}
}

// On the night light a camera view is tapped away too: a picture notification a touch released (which
// ends the notification), and a view somebody asked for.
func TestACameraViewIsTappedAwayOnTheNightLight(t *testing.T) {
	d := noticeDisplay(t, showWide, showHigh)
	atNight(t)
	if err := config.Set().Screen().AtNight(true, true); err != nil {
		t.Fatal(err)
	}
	d.nightGlow = true
	at := time.Now()
	_, redOnly := nightScreen(t, d, at)

	if err := notify.Get().NotifyPicture("Someone is here", "Doorbell", "camera.front_door", "", "", "", "", 600); err != nil {
		t.Fatal(err)
	}
	if _, held := nightScreen(t, d, at); !samePixels(held, redOnly) {
		t.Fatal("a picture notification took the night screen before a touch")
	}
	tapAt(d, image.Pt(5, 5)) // releases it
	if _, shown := nightScreen(t, d, at); samePixels(shown, redOnly) {
		t.Fatal("the released picture is not drawn")
	}
	if !noticeUp() {
		t.Fatal("the touch that released the picture ended it")
	}
	listenForNotice()
	tapAt(d, image.Pt(showWide/2, showHigh/2))
	dismissedByTap(t, "a tap on the released picture")
	within(t, "the view coming down", func() bool { _, up := home.Get().Camera(); return !up })
	if _, after := nightScreen(t, d, at); !samePixels(after, redOnly) {
		t.Error("the red clock did not come back after the picture was tapped away")
	}

	// A view somebody asked for is not held, and a tap takes it down.
	home.Get().ShowCamera("camera.deck", time.Minute)
	if _, shown := nightScreen(t, d, at); samePixels(shown, redOnly) {
		t.Fatal("a camera view asked for is not drawn on the night light")
	}
	tapAt(d, image.Pt(showWide/2, showHigh/2))
	within(t, "the plain view coming down", func() bool { _, up := home.Get().Camera(); return !up })
}

// On the night light a tap reaches the camera page's controls before it closes the view: somebody
// reaching for Talk at the door at night must not lose the doorbell's view, and Mute likewise. A tap
// anywhere else still takes the view down.
func TestTheNightLightKeepsACameraViewForTalkAndMute(t *testing.T) {
	d := noticeDisplay(t, showWide, showHigh)
	atNight(t)
	if err := config.Set().Screen().AtNight(true, true); err != nil {
		t.Fatal(err)
	}
	d.nightGlow = true
	var talked, muted atomic.Int32
	toggleTalk = func(string) { talked.Add(1) }
	toggleSound = func() { muted.Add(1) }
	t.Cleanup(func() {
		toggleTalk = func(entity string) { talkback.Get().Toggle(entity) }
		toggleSound = func() { home.Get().ToggleCameraSound() }
	})

	home.Get().ShowCamera("camera.front_door", time.Minute)
	v, _ := home.Get().Camera()
	d.r.draw(scene{now: time.Now(), phase: "idle", showCamera: true, camera: v, talkOffered: true, cameraSound: true})
	mid := func(b image.Rectangle) image.Point { return b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2)) }

	tapAt(d, mid(d.r.cameraTalkAt))
	within(t, "Talk toggling", func() bool { return talked.Load() == 1 })
	tapAt(d, mid(d.r.cameraSoundAt))
	within(t, "Mute toggling", func() bool { return muted.Load() == 1 })
	time.Sleep(100 * time.Millisecond) // a close, if one were wrongly started, has had its turn
	if _, up := home.Get().Camera(); !up {
		t.Fatal("a tap on Talk or Mute closed the camera view on the night light")
	}

	tapAt(d, image.Pt(showWide/2, showHigh/2))
	within(t, "the view coming down", func() bool { _, up := home.Get().Camera(); return !up })
	if talked.Load() != 1 || muted.Load() != 1 {
		t.Errorf("a tap elsewhere toggled talk %d and sound %d, want 1 and 1", talked.Load(), muted.Load())
	}
}

// A tap that stops a ring is for the alarm: it does not release a notification that is waiting.
func TestATapThatStopsARingDoesNotReleaseANotification(t *testing.T) {
	d := noticeDisplay(t, showWide, showHigh)
	atNight(t)
	if err := config.Set().Screen().AtNight(true, true); err != nil {
		t.Fatal(err)
	}
	d.nightGlow = true
	d.ringPreview = time.Now().Add(time.Minute) // something ringing
	if err := notify.Get().Notify("The washer is done", "", "", "", "", "", 600); err != nil {
		t.Fatal(err)
	}
	tapAt(d, image.Pt(5, 5))
	if !d.noticeHeld() {
		t.Error("the tap that stopped the ring released the notification")
	}
	d.ringPreview = time.Time{}
	tapAt(d, image.Pt(5, 5))
	if d.noticeHeld() {
		t.Error("a touch with nothing ringing did not release it")
	}
}

// The first touch is not a tap on a card nobody has seen.
func TestTheTouchThatShowsANotificationDoesNotDismissIt(t *testing.T) {
	d := noticeDisplay(t, showWide, showHigh)
	atNight(t)
	d.on = true
	if err := notify.Get().Notify("The washer is done", "", "", "", "", "", 600); err != nil {
		t.Fatal(err)
	}
	tapAt(d, image.Pt(showWide/2, showHigh/2))
	time.Sleep(50 * time.Millisecond)
	if !noticeUp() {
		t.Fatal("the touch that showed the card dismissed it")
	}
	listenForNotice()
	tapAt(d, image.Pt(showWide/2, showHigh/2))
	dismissedByTap(t, "a tap on the shown card")
}

// A picture notification's camera page waits for the touch the same way.
func TestAPictureNotificationWaitsOutTheNightToo(t *testing.T) {
	d := noticeDisplay(t, showWide, showHigh)
	atNight(t)
	if err := notify.Get().NotifyPicture("Someone is here", "Doorbell", "camera.front_door", "", "", "", "", 600); err != nil {
		t.Fatal(err)
	}
	v, up := home.Get().Camera()
	if !up {
		t.Fatal("the picture's view is not up")
	}
	if _, shown := d.gateCamera(v, up, true); shown {
		t.Error("a picture notification took the night screen before a touch")
	}
	tapAt(d, image.Pt(5, 5))
	if _, shown := d.gateCamera(v, up, true); !shown {
		t.Error("the picture did not show after a touch")
	}
	if _, shown := d.gateCamera(home.CameraView{Entity: "camera.deck"}, true, true); !shown {
		t.Error("a camera the owner asked for was held at night")
	}
}

// The caption's words are light on the shade in every theme: the theme's text color is dark on Paper
// and Linen, and the shade is black.
func TestACaptionIsReadableInEveryTheme(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	saved := current()
	t.Cleanup(func() { applyTheme(saved) })
	for _, panel := range showPanels() {
		for _, name := range []string{"Paper", "Linen"} {
			th := themes[themeIndex(name)]
			if th.name != name {
				t.Fatalf("no %s theme", name)
			}
			applyTheme(th)
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			dark := image.NewUniform(color.RGBA{A: 0xff}) // a dark picture under the bar
			for y := 0; y < panel.high; y++ {
				for x := 0; x < panel.wide; x++ {
					img.Set(x, y, dark.C)
				}
			}
			s := scene{now: at, phase: "idle", showCamera: true, camera: home.CameraView{Entity: "camera.front_door",
				Name: "Front door", Until: at.Add(30 * time.Second), Frame: img,
				Caption: &home.Caption{ID: "1", Title: "Doorbell", Message: "Someone is at the front door"}}}
			out := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			r := newRenderer(out)
			r.draw(s)
			bar := r.captionRect(*s.camera.Caption)
			var bright, mid int
			for y := bar.Min.Y; y < bar.Max.Y; y++ {
				for x := bar.Min.X; x < bar.Max.X; x++ {
					c := out.RGBAAt(x, y)
					lum := (int(c.R)*299 + int(c.G)*587 + int(c.B)*114) / 1000
					if lum > 150 {
						bright++
					} else if lum > 40 {
						mid++
					}
				}
			}
			if bright < 200 {
				t.Errorf("%s %s: only %d light pixels in the caption bar; its words are not light on the shade", panel.name, name, bright)
			}
			// No dark words: the only pixels in the bar are the shade's black and light text (and the
			// edges between them).
			if mid > bright*2 {
				t.Errorf("%s %s: %d mid-tone pixels against %d light ones; the words are drawn in a dark color", panel.name, name, mid, bright)
			}
		}
	}
}

// The bar fits the words in the fonts the panel draws them in, and stays clear of the Mute and Talk
// boxes at the foot of the page.
func TestTheCaptionBarFitsItsFonts(t *testing.T) {
	for _, panel := range showPanels() {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
		for _, c := range []home.Caption{{ID: "1", Message: "m"}, {ID: "1", Title: "t", Message: "m"}} {
			bar := r.captionRect(c)
			need := r.small.Metrics().Height.Ceil()
			if c.Title != "" {
				need += r.tiny.Metrics().Height.Ceil()
			}
			if bar.Dy() < need+2*r.s(4) {
				t.Errorf("%s: the bar for %+v is %d high, the words alone are %d", panel.name, c, bar.Dy(), need)
			}
			for _, label := range []string{"Mute", "Unmute", "Talk", "Talking…"} {
				if box := r.cameraSoundBox(label); box.Overlaps(bar) {
					t.Errorf("%s: the %q box %v reaches into the caption bar %v", panel.name, label, box, bar)
				}
			}
		}
	}
}

// With SHOW_PREVIEW set to a directory, the notification card and the captioned camera page are
// written there in both Shows and the two light themes, to look at.
func TestNoticePreviewsInTheLightThemes(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	if dir == "" {
		t.Skip("SHOW_PREVIEW is not set")
	}
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	saved := current()
	t.Cleanup(func() { applyTheme(saved) })
	for _, panel := range showPanels() {
		for _, name := range []string{"Paper", "Linen"} {
			applyTheme(themes[themeIndex(name)])
			pic := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			for y := 0; y < panel.high; y++ {
				for x := 0; x < panel.wide; x++ {
					v := uint8(150 + 90*x/panel.wide) // a bright picture: the demanding ground for the words
					pic.Set(x, y, color.RGBA{v, v, v - 20, 0xff})
				}
			}
			for file, s := range map[string]scene{
				"notice-card": {now: at, phase: "idle", showNotification: true, notification: notify.Notification{
					Kind: notify.Text, Title: "Washer", Icon: "mdi:washing-machine", Message: "The washing machine is done"}},
				"notice-camera": {now: at, phase: "idle", showCamera: true, cameraSound: true, cameraSoundLive: true,
					camera: home.CameraView{Entity: "camera.front_door", Name: "Front door", Until: at.Add(30 * time.Second),
						Frame: pic, Caption: &home.Caption{ID: "1", Title: "Doorbell",
							Message: "Someone is at the front door and has been there a while, ringing the bell twice more"}}},
			} {
				img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
				newRenderer(img).draw(s)
				f, err := os.Create(filepath.Join(dir, file+"-"+name+"-"+panel.name+".png"))
				if err != nil {
					t.Fatal(err)
				}
				if err := png.Encode(f, img); err != nil {
					t.Fatal(err)
				}
				f.Close()
			}
		}
	}
}
