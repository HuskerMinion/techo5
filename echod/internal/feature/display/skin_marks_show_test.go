//go:build !dot && !spot

package display

import (
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/talkback"
)

// Under a one-color skin the Show takes the filter off for a frame with a mark whose color means
// something - the header's "microphone off" pill, Talk red while live - and only then: a muted device
// on the settings sheet, which has no pill, and Talk not yet tapped keep the skin.
func TestTheShowUnskinsOnlyWhileItsMarksAreUp(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	const door = "camera.front_door"
	view := home.CameraView{Entity: door, Name: "Front door", Frame: testPhoto("sky", 860, 430), Until: at.Add(time.Hour)}
	for _, c := range []struct {
		name   string
		s      scene
		marked bool
	}{
		{"clock", scene{}, false},
		{"muted", scene{muted: true}, true},
		{"muted, settings sheet", scene{muted: true, showSheet: true}, false},
		{"camera, Talk offered", scene{showCamera: true, camera: view, talkOffered: true}, false},
		{"camera, Talk connecting", scene{showCamera: true, camera: view, talkOffered: true,
			talk: talkback.State{Entity: door, Phase: talkback.Opening}}, true},
		{"camera, Talk live", scene{showCamera: true, camera: view, talkOffered: true,
			talk: talkback.State{Entity: door, Phase: talkback.Talking, Left: time.Minute}}, true},
	} {
		c.s.now, c.s.phase = at, "idle"
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
		r.marked = !c.marked // what the frame before left must not count
		r.draw(c.s)
		if r.marked != c.marked {
			t.Errorf("%s: marked = %v, want %v", c.name, r.marked, c.marked)
		}
	}
}
