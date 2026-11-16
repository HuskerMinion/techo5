//go:build !dot && !spot

package display

import (
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
	"github.com/HuskerMinion/techo5/echod/internal/feature/remind"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// One card is up at a time, a reminder over a notification over an event's pop-up, and the one up
// counts what waits under it: what is above a card is not under it.
func TestACardCountsTheCardsUnderIt(t *testing.T) {
	event := &hass.Event{Summary: "Dentist"}
	for _, c := range []struct {
		name string
		s    scene
		want int
	}{
		{"a reminder alone", scene{showReminder: true}, 0},
		{"a notification alone", scene{showNotification: true}, 0},
		{"a pop-up alone", scene{popup: event}, 0},
		{"a reminder over a notification", scene{showReminder: true, showNotification: true}, 1},
		{"a reminder over a pop-up", scene{showReminder: true, popup: event}, 1},
		{"a notification over a pop-up and one waiting", scene{showNotification: true, popup: event, popupWaiting: 1}, 2},
		{"a pop-up with two waiting", scene{popup: event, popupWaiting: 2}, 2},
		{"all three and a pop-up waiting", scene{showReminder: true, showNotification: true, popup: event, popupWaiting: 1}, 3},
	} {
		if got := cardsUnder(c.s); got != c.want {
			t.Errorf("%s: %d under it, want %d", c.name, got, c.want)
		}
	}
}

// The count is drawn in the heading row and nowhere else: a reminder with a notification under it
// differs from the reminder alone only between the heading and the way out, and a card with nothing
// under it draws no count at all.
func TestACardSaysHowManyWaitUnderIt(t *testing.T) {
	at := time.Date(2025, 9, 10, 14, 7, 0, 0, time.Local)
	r := testRenderer()
	frame := func(s scene) *image.RGBA {
		r.draw(s)
		return &image.RGBA{Pix: append([]uint8(nil), r.dst.Pix...), Stride: r.dst.Stride, Rect: r.dst.Rect}
	}
	alone := scene{now: at, phase: "idle", showReminder: true, reminder: remind.Reminder{Label: "Take the trash out"}}
	over := alone
	over.showNotification = true
	over.notification = notify.Notification{Kind: notify.Text, Message: "under the reminder"}

	a := frame(alone)
	b := frame(over)
	box := r.reminderBox()
	changed := image.Rectangle{}
	for y := 0; y < r.h; y++ {
		for x := 0; x < r.w; x++ {
			if a.RGBAAt(x, y) != b.RGBAAt(x, y) {
				changed = changed.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	if changed.Empty() {
		t.Fatal("a reminder over a notification looks the same as a reminder alone")
	}
	hintLeft := box.Max.X - r.rowIn() - r.width(r.tiny, dismissHint())
	row := image.Rect(box.Min.X+r.rowIn(), box.Min.Y, hintLeft, box.Min.Y+r.s(56)+r.s(12))
	if !changed.In(row) {
		t.Errorf("the count changed %v, outside the heading row left of the way out %v", changed, row)
	}

	if again := frame(alone); string(again.Pix) != string(a.Pix) {
		t.Error("a card with nothing under it changed after one that had: the count was kept")
	}
}
