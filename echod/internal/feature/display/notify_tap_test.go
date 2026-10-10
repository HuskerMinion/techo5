//go:build !dot && !spot

package display

import (
	"image"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
)

// The card is the only part of the screen a notification owns: a tap on it dismisses it, a tap on
// the clock beside it is a tap on the clock.
func TestWhereATapEndsANotification(t *testing.T) {
	for _, panel := range showPanels() {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
		box := r.notifyBox()
		if !image.Pt(panel.wide/2, panel.high/2).In(box) {
			t.Errorf("%s: the middle of the screen is not on the card %v", panel.name, box)
		}
		for _, p := range []image.Point{{4, 4}, {panel.wide - 4, panel.high - 4}, {box.Min.X - 4, box.Min.Y + 10}} {
			if p.In(box) {
				t.Errorf("%s: %v, off the card, counts as on it", panel.name, p)
			}
		}
	}
}

// A notification puts the deck away while it is up, as a reminder does.
func TestANotificationPutsTheDeckAway(t *testing.T) {
	s := scene{showDeck: true}
	if deckFrameKey(s, false, false) == "" {
		t.Fatal("the deck alone has no frame key; this test proves nothing")
	}
	s.showNotification, s.notification = true, notify.Notification{Kind: notify.Text, Message: "m"}
	if deckFrameKey(s, false, false) != "" {
		t.Error("the deck is drawn as itself with a notification over it")
	}
}
