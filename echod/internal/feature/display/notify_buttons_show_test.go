//go:build !dot && !spot

package display

import (
	"image"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
)

func buttonsNotification(size notify.Size, critical bool, buttons ...notify.Button) notify.Notification {
	return notify.Notification{
		ID: "7", Kind: notify.Text, Title: "Leak", Icon: "mdi:water-alert", Size: size, Critical: critical,
		Message: "Water under the kitchen sink, and more words than fit on three lines of the card so it is cut",
		Buttons: buttons,
	}
}

// A card's buttons sit side by side inside it, along its foot, apart from one another, on every Show and
// at every size; a usual card grows to hold them, and one without them is the usual card still.
func TestACardsButtonsSitAlongItsFoot(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	three := []notify.Button{{Label: "Snooze ten minutes", ID: "a"}, {Label: "Turn the water off now", ID: "b"}, {ID: notify.OKID}}
	for _, panel := range showPanels() {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
		screen := image.Rect(0, 0, panel.wide, panel.high)
		for _, size := range []notify.Size{notify.SizeNormal, notify.SizeLarge, notify.SizeHuge} {
			for k := 1; k <= len(three); k++ {
				n := buttonsNotification(size, k == 1, three[:k]...)
				box, bs := r.notifyBoxFor(n), r.notifyButtons(n)
				if !box.In(screen) {
					t.Errorf("%s %v: the card %v is off the screen", panel.name, size, box)
				}
				if len(bs) != k {
					t.Fatalf("%s %v: %d buttons laid out, want %d", panel.name, size, len(bs), k)
				}
				for i, b := range bs {
					if !b.In(box) || b.Dy() < r.s(notifyButtonH) || b.Min.Y < box.Max.Y-r.notifyFoot() {
						t.Errorf("%s %v: button %d at %v, not along the foot of %v", panel.name, size, i, b, box)
					}
					if i > 0 && bs[i-1].Max.X >= b.Min.X {
						t.Errorf("%s %v: buttons %d and %d meet", panel.name, size, i-1, i)
					}
				}
				// Drawing it must not fail, long labels and all.
				r.notifyCard(scene{notification: n, showNotification: true})
				if r.hintOff {
					t.Error("the hint stayed off after a critical card")
				}
			}
		}
		plain := notify.Notification{Kind: notify.Text, Message: "m"}
		if r.notifyBoxFor(plain) != r.notifyBox() || r.notifyButtons(plain) != nil {
			t.Errorf("%s: a card without buttons changed", panel.name)
		}
	}
}

// At night a critical notification is not held for a touch: it is drawn over the night clock as by day.
// One with buttons is not drawn on the night clock (Night clock shows notifications), which has no room
// for them, but held as on any other night.
func TestACriticalCardIsNotHeldAtNight(t *testing.T) {
	d := noticeDisplay(t, 1024, 600)
	s := scene{redClock: true, nightInfo: true, showNotification: true, notification: buttonsNotification(notify.SizeNormal, true, notify.Button{ID: notify.OKID})}
	d.gateNotice(&s, true)
	if !s.showNotification || s.redClock || s.nightInfo {
		t.Errorf("critical at night: shown %v, red clock %v, night info %v", s.showNotification, s.redClock, s.nightInfo)
	}
	s = scene{redClock: true, nightInfo: true, showNotification: true, notification: buttonsNotification(notify.SizeNormal, false, notify.Button{Label: "Open", ID: "open"})}
	s.notification.ID = "8"
	d.gateNotice(&s, true)
	if s.showNotification || s.nightInfo {
		t.Errorf("buttons at night: shown %v, night info %v; want held, off the night clock", s.showNotification, s.nightInfo)
	}
}
