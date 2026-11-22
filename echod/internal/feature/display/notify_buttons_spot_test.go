//go:build spot

package display

import (
	"image"
	"math"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
)

// The face draws two buttons at most, inside the circle, apart; a critical one is not held at night.
func TestTheFacesButtonsAreInsideTheCircle(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	r := newRoundRenderer(image.NewRGBA(image.Rect(0, 0, side, side)))
	n := notify.Notification{
		ID: "7", Kind: notify.Text, Title: "Leak", Icon: "mdi:water-alert", Critical: true,
		Message: "Water under the kitchen sink, and more words than fit on two lines of the face",
		Buttons: []notify.Button{{Label: "Snooze ten minutes", ID: "a"}, {Label: "Off", ID: "b"}, {ID: notify.OKID}},
	}
	bs := r.notifyButtons(n)
	if len(bs) != notifyFaceButtons {
		t.Fatalf("%d buttons on the face, want %d", len(bs), notifyFaceButtons)
	}
	for i, b := range bs {
		for _, p := range []image.Point{b.Min, {b.Max.X, b.Min.Y}, {b.Min.X, b.Max.Y}, b.Max} {
			if math.Hypot(float64(p.X-center), float64(p.Y-center)) > rimIn {
				t.Errorf("button %d's corner %v is outside the circle", i, p)
			}
		}
	}
	if bs[0].Max.X >= bs[1].Min.X {
		t.Error("the buttons meet")
	}
	r.notifyFace(roundScene{notification: n, showNotification: true})

	d := spotNoticeDisplay(t, true)
	s := roundScene{phase: "idle", redClock: true, nightInfo: true, showNotification: true, notification: n}
	d.gateNotice(&s, true)
	if !s.showNotification || s.nightInfo || s.nightClockFace() {
		t.Errorf("critical at night: shown %v, night info %v, night face %v", s.showNotification, s.nightInfo, s.nightClockFace())
	}
}
