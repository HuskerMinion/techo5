//go:build spot

package display

import (
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
)

// On the Spot the setting is the notification alone (it has no Next up card or chips): at night it is
// drawn at once on the night clock in the night's palette, tapped away there, and held for a touch as
// before when the setting is off.
func TestNightInfoShowsANotificationOnTheSpot(t *testing.T) {
	d := spotNoticeDisplay(t, true)
	if err := config.Set().Screen().AtNight(true, true); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Screen().NightInfo(true); err != nil {
		t.Fatal(err)
	}
	d.nightGlow = true
	at := time.Now()
	frame := func(info bool) (roundScene, *image.RGBA) {
		s := roundScene{now: at, phase: "idle", redClock: true, nightInfo: info}
		s.notification, s.showNotification = notify.Get().Card()
		d.gateNotice(&s, nightNow(at))
		img := image.NewRGBA(image.Rect(0, 0, side, side))
		newRoundRenderer(img).draw(s)
		d.mu.Lock()
		d.redShown = s.nightClockFace()
		d.mu.Unlock()
		return s, img
	}
	_, clock := frame(true)
	if err := notify.Get().Notify("The server is down", "Server", "", "", "", "", 600); err != nil {
		t.Fatal(err)
	}
	s, shown := frame(true)
	if !s.showNotification || !s.nightClockFace() {
		t.Fatalf("showNotification=%v nightClockFace=%v, want the card on the night clock", s.showNotification, s.nightClockFace())
	}
	if samePixelsSpot(shown, clock) {
		t.Fatal("the notification is not drawn")
	}
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			if c := shown.RGBAAt(x, y); c.R > nightRed.R || c.G > nightRed.G || c.B > 0 {
				t.Fatalf("a pixel the night cannot make at %d,%d: %v", x, y, c)
			}
		}
	}
	if d.noticeHeld() {
		t.Error("the notification is waiting for a touch")
	}
	spotTap(d)
	spotWithin(t, "the tap putting the card away", func() bool { return !spotNoticeUp() })
	d.mu.Lock()
	glowing := d.nightGlow
	d.mu.Unlock()
	if !glowing {
		t.Error("the tap that put the card away lifted the night light")
	}

	// Off, it is held for a touch.
	if err := notify.Get().Notify("Held", "", "", "", "", "", 600); err != nil {
		t.Fatal(err)
	}
	if s, _ := frame(false); s.showNotification {
		t.Error("with the setting off the notification was not held")
	}
}
