//go:build spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// A notification's picture on the round face says how to close it, at the foot, where the sound's
// control goes; a plain camera does not, and neither does a picture with that control drawn in the place.
// A picture with no words has no name band either.
func TestANotificationPictureSaysHowToCloseIt(t *testing.T) {
	at := time.Date(2026, 10, 9, 14, 7, 0, 0, time.Local)
	frame := image.NewRGBA(image.Rect(0, 0, side, side))
	draw.Draw(frame, frame.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	face := func(c *home.Caption, sound bool) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, side, side))
		newRoundRenderer(img).draw(roundScene{now: at, phase: "idle", showCamera: true, cameraSound: sound,
			camera: home.CameraView{Entity: "camera.front_door", Name: "Front door", Frame: frame, Caption: c}})
		return img
	}
	foot := cameraSoundBox(96)
	footMid := image.Pt(foot.Min.X+6, foot.Min.Y+foot.Dy()/2) // the band's left end, clear of its words
	isWhite := func(img *image.RGBA, p image.Point) bool {
		return img.RGBAAt(p.X, p.Y) == color.RGBA{255, 255, 255, 255}
	}

	if isWhite(face(&home.Caption{ID: "1"}, false), footMid) {
		t.Error("a notification's picture has no tap to close at its foot")
	}
	if !isWhite(face(nil, false), footMid) {
		t.Error("a plain camera grew a tap to close")
	}
	if !isWhite(face(&home.Caption{ID: "1"}, false), image.Pt(center, 62)) {
		t.Error("a picture sent with no words has a name band")
	}
	if isWhite(face(&home.Caption{ID: "1", Title: "Doorbell"}, false), image.Pt(center, 62)) {
		t.Error("a picture with a title lost its name band")
	}
}
