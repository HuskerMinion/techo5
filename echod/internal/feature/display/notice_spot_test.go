//go:build spot

package display

import (
	"fmt"
	"image"
	"path/filepath"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
)

// spotNoticeDisplay is a Display with a lit face and a light to turn on and off, and a notifier that
// keeps quiet, so a test can put a notification up without a chime. The hours are set round now: night
// when asked, otherwise a window that has not started.
func spotNoticeDisplay(t *testing.T, night bool) *Display {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	from := time.Now().Hour()
	if !night {
		from = (from + 3) % 24
	}
	if err := config.Set().Screen().Night(fmt.Sprintf("%d-%d", from, (from+2)%24)); err != nil {
		t.Fatal(err)
	}
	if inNight(time.Now()) != night {
		t.Fatalf("inNight is not %v", night)
	}
	notify.SetNight(func(time.Time) bool { return true })
	t.Cleanup(func() {
		notify.Get().Dismiss()
		home.Get().HideCamera()
		notify.SetNight(func(time.Time) bool { return false })
	})
	return &Display{
		r: newRoundRenderer(image.NewRGBA(image.Rect(0, 0, side, side))), on: true, poke: make(chan struct{}, 1),
		view: voice.State{Phase: "idle"},
		light: &esphome.Light{
			Base:                esphome.Base{ObjectID: "screen", Name: "Screen", Icon: "mdi:monitor"},
			SupportedColorModes: []esphome.ColorMode{esphome.ColorModeBrightness},
		},
	}
}

func spotNoticeUp() bool { _, up := notify.Get().Showing(); return up }

func spotTap(d *Display) { d.gesture(touch.Gesture{Kind: touch.Tap, X: center, Y: center}) }

func spotWithin(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("%s did not happen", what)
}

// spotFace is what the frame would draw for the notification and camera view up now, as the gates leave
// them.
func spotFace(d *Display, at time.Time) (roundScene, *image.RGBA) {
	s := roundScene{now: at, phase: "idle"}
	s.camera, s.showCamera = home.Get().Camera()
	s.notification, s.showNotification = notify.Get().Card()
	d.gateNotice(&s, inNight(at))
	s.camera, s.showCamera = d.gateCamera(s.camera, s.showCamera, inNight(at))
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	newRoundRenderer(img).draw(s)
	return s, img
}

func samePixelsSpot(a, b *image.RGBA) bool { return string(a.Pix) == string(b.Pix) }

// A camera view somebody asked for (a doorbell) wins over an unread card, in the face and in the
// taps; the card stays up underneath and shows when the view ends.
func TestACameraViewWinsOverACard(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	card := notify.Notification{Kind: notify.Text, Title: "Washer", Message: "The washing machine is done"}
	view := home.CameraView{Entity: "camera.front_door", Name: "Front door", Frame: testPicture()}
	draw := func(s roundScene) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, side, side))
		newRoundRenderer(img).draw(s)
		return img
	}
	cameraOnly := draw(roundScene{now: at, phase: "idle", showCamera: true, camera: view})
	cardOnly := draw(roundScene{now: at, phase: "idle", showNotification: true, notification: card})
	both := draw(roundScene{now: at, phase: "idle", showCamera: true, camera: view, showNotification: true, notification: card})
	if samePixelsSpot(cardOnly, cameraOnly) {
		t.Fatal("the card and the camera view draw alike, so the test shows nothing")
	}
	if !samePixelsSpot(both, cameraOnly) {
		t.Error("with a camera view and a card up, the face is not the camera view")
	}

	d := spotNoticeDisplay(t, false)
	if err := notify.Get().Notify("The washer is done", "Washer", "", "", "", "", 600); err != nil {
		t.Fatal(err)
	}
	home.Get().ShowCamera("camera.front_door", time.Minute)
	if _, up := home.Get().Camera(); !up {
		t.Fatal("the camera view is not up")
	}
	spotTap(d)
	spotWithin(t, "the camera view being closed", func() bool { _, up := home.Get().Camera(); return !up })
	time.Sleep(50 * time.Millisecond)
	if !spotNoticeUp() {
		t.Fatal("the tap that closed the camera view dismissed the card under it")
	}
	s, _ := spotFace(d, time.Now())
	if !s.showNotification {
		t.Error("the card did not show when the camera view ended")
	}
	spotTap(d)
	spotWithin(t, "the card being dismissed", func() bool { return !spotNoticeUp() })
}

// A dark face is lit for a notification once, by day: a screen turned off while it is up stays off, and
// at night nothing lights it.
func TestANotificationLightsADarkFaceOnce(t *testing.T) {
	d := spotNoticeDisplay(t, false)
	d.on = false
	if err := notify.Get().Notify("The washer is done", "", "", "", "", "", 60); err != nil {
		t.Fatal(err)
	}
	if d.noticeLights(false, true) {
		t.Fatal("a face was lit for a notification at night")
	}
	if !d.noticeLights(false, false) || !d.on {
		t.Fatal("a notification by day did not light a dark face")
	}
	d.on = false // turned off from Home Assistant, or by an empty room
	if d.noticeLights(false, false) || d.on {
		t.Error("a face turned off with the notification still up was lit again")
	}
	if err := notify.Get().Notify("The dryer is done", "", "", "", "", "", 60); err != nil {
		t.Fatal(err)
	}
	if !d.noticeLights(false, false) || !d.on {
		t.Error("the next notification did not light the face")
	}
}

// Calling the Changed handler again and again does not light the face again either.
func TestNotifyLightsLightsOncePerNotification(t *testing.T) {
	d := spotNoticeDisplay(t, false)
	d.on = false
	if err := notify.Get().Notify("The washer is done", "", "", "", "", "", 60); err != nil {
		t.Fatal(err)
	}
	d.notifyLights()
	if !d.on {
		t.Fatal("a notification by day did not light a dark face")
	}
	d.on = false
	d.notifyLights()
	if d.on {
		t.Error("a face turned off with the notification up was lit again by the next change")
	}
}

// At night a notification waits: the face stays as it is, and the first touch shows the card and does
// nothing else. A tap on the card then puts it away.
func TestANotificationWaitsOutTheNightForATouchOnTheSpot(t *testing.T) {
	d := spotNoticeDisplay(t, true)
	at := time.Now()
	_, plain := spotFace(d, at)

	if err := notify.Get().Notify("The washer is done", "Washer", "", "", "", "", 600); err != nil {
		t.Fatal(err)
	}
	s, held := spotFace(d, at)
	if s.showNotification {
		t.Fatal("a fresh notification at night took the face")
	}
	if !samePixelsSpot(held, plain) {
		t.Fatal("the held notification changed what the night face draws")
	}

	spotTap(d) // the first touch
	time.Sleep(50 * time.Millisecond)
	if !spotNoticeUp() {
		t.Fatal("the touch that showed the card dismissed it")
	}
	s, shown := spotFace(d, at)
	if !s.showNotification || samePixelsSpot(shown, plain) {
		t.Fatal("after a touch the card is not drawn")
	}

	spotTap(d)
	spotWithin(t, "a tap on the shown card putting it away", func() bool { return !spotNoticeUp() })

	// The next one is held again, and one that came by day is not.
	if err := notify.Get().Notify("The dryer is done", "", "", "", "", "", 600); err != nil {
		t.Fatal(err)
	}
	if s, _ = spotFace(d, at); s.showNotification {
		t.Error("a second notification at night was drawn without a touch")
	}
	card, up := notify.Get().Card()
	s = roundScene{now: at, phase: "idle", notification: card, showNotification: up}
	d.gateNotice(&s, false)
	if !s.showNotification {
		t.Error("a notification by day was held")
	}
}

// A card seen by day stays when the night begins.
func TestANotificationSeenByDayStaysThroughTheNight(t *testing.T) {
	d := spotNoticeDisplay(t, false)
	if err := notify.Get().Notify("The washer is done", "", "", "", "", "", 600); err != nil {
		t.Fatal(err)
	}
	s, _ := spotFace(d, time.Now())
	if !s.showNotification {
		t.Fatal("the card was held by day")
	}
	card, up := notify.Get().Card()
	s = roundScene{now: time.Now(), phase: "idle", notification: card, showNotification: up}
	d.gateNotice(&s, true)
	if !s.showNotification {
		t.Error("the night took away a card that was already up")
	}
}

// On a dark face the first touch lights it and releases the notification; at night a menu is not
// closed under a held one, and the touch that releases it is not a tap on anything else.
func TestTheTouchThatReleasesANotificationOnlyReleasesIt(t *testing.T) {
	d := spotNoticeDisplay(t, true)
	d.menuOpen = true
	if err := notify.Get().Notify("The washer is done", "", "", "", "", "", 600); err != nil {
		t.Fatal(err)
	}
	d.notifyLights()
	if !d.menuOpen {
		t.Error("a held notification closed the menu at night")
	}
	if !d.noticeHeld() {
		t.Fatal("the notification is not held")
	}
	d.gesture(touch.Gesture{Kind: touch.SwipeLeft, X: center, Y: center})
	if d.noticeHeld() {
		t.Error("a touch did not release the notification")
	}
	if d.menuOpen {
		t.Error("the released card sits behind an open menu")
	}
	if !spotNoticeUp() {
		t.Error("the releasing touch dismissed it")
	}

	// A dark face: the touch lights it, releases the notification, and is spent.
	d2 := spotNoticeDisplay(t, true)
	d2.on = false
	if err := notify.Get().Notify("The dryer is done", "", "", "", "", "", 600); err != nil {
		t.Fatal(err)
	}
	if d2.noticeLights(false, true) {
		t.Fatal("lit at night")
	}
	spotTap(d2)
	if !d2.on {
		t.Error("the touch did not light the dark face")
	}
	if d2.noticeHeld() {
		t.Error("the touch that lit the face did not release the notification")
	}
	if !spotNoticeUp() {
		t.Error("the touch that lit the face dismissed the notification")
	}
}

// A picture notification's camera view waits for the touch the same way, and a view somebody asked for
// is not held.
func TestAPictureNotificationWaitsOutTheNightOnTheSpot(t *testing.T) {
	d := spotNoticeDisplay(t, true)
	if err := notify.Get().NotifyPicture("Someone is here", "Doorbell", "camera.front_door", "", "", "", "", 600); err != nil {
		t.Fatal(err)
	}
	v, up := home.Get().Camera()
	if !up {
		t.Fatal("the picture's view is not up")
	}
	if _, shown := d.gateCamera(v, up, true); shown {
		t.Error("a picture notification took the night face before a touch")
	}
	spotTap(d)
	if _, shown := d.gateCamera(v, up, true); !shown {
		t.Error("the picture did not show after a touch")
	}
	if _, shown := d.gateCamera(home.CameraView{Entity: "camera.deck"}, true, true); !shown {
		t.Error("a camera the owner asked for was held at night")
	}
}

// A face the menu put to sleep, at night: the touch that lights it and releases the notification puts the
// menu away too, so it does not come back behind the card once the card is dismissed.
func TestTheTouchThatLightsADarkFacePutsTheMenuAway(t *testing.T) {
	d := spotNoticeDisplay(t, true)
	d.on, d.menuOpen = false, true
	if err := notify.Get().Notify("The washer is done", "", "", "", "", "", 600); err != nil {
		t.Fatal(err)
	}
	spotTap(d)
	if !d.on || d.noticeHeld() {
		t.Fatalf("the touch did not light the face and release the notification (on=%v)", d.on)
	}
	if d.menuOpen {
		t.Error("the menu stayed open behind the released card")
	}
}
