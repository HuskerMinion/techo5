//go:build spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The round camera page's action buttons: stacked above Talk and the sound's bar, inside the circle,
// each tappable where it is drawn and none taking a tap meant for Talk or the sound. With SPOT_PREVIEW
// set, each is written there to look at.
func TestTheRoundCameraButtons(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	dir := os.Getenv("SPOT_PREVIEW")
	view := home.CameraView{Entity: "camera.front_door", Name: "Front door", Frame: testPicture(), Until: at.Add(time.Hour)}
	four := []camButtonFace{{label: "Open gate"}, {label: "Tap again: Unlock", armed: true},
		{label: "No Home Assistant", failed: true}, {label: "A label of twenty-four ch"}}
	scenes := map[string]roundScene{
		"camera-buttons":      {talkOffered: true, cameraSound: true, camButtons: four[:2]},
		"camera-buttons-four": {talkOffered: true, cameraSound: true, camButtons: four},
		"camera-buttons-bare": {camButtons: four[:1]},
	}
	for name, s := range scenes {
		s.now, s.phase, s.showCamera, s.camera = at, "idle", true, view
		img := image.NewRGBA(image.Rect(0, 0, side, side))
		r := newRoundRenderer(img)
		r.draw(s)
		if len(r.cameraButtonsAt) != len(s.camButtons) {
			t.Fatalf("%s: %d buttons drawn, want %d", name, len(r.cameraButtonsAt), len(s.camButtons))
		}
		for i, b := range r.cameraButtonsAt {
			mid := b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2))
			if got := r.cameraButtonTapped(mid.X, mid.Y); got != i {
				t.Errorf("%s: a tap on button %d at %v found %d", name, i, mid, got)
			}
			if r.cameraTalkTapped(mid.X, mid.Y) || r.cameraSoundTapped(mid.X, mid.Y) {
				t.Errorf("%s: a tap on button %d was taken for Talk or the sound", name, i)
			}
			if b.Min.X < center-rimIn || b.Max.X > center+rimIn || b.Min.Y < 40 {
				t.Errorf("%s: button %d at %v, off the face", name, i, b)
			}
		}
		for _, c := range []image.Rectangle{r.cameraTalkAt, r.cameraSoundAt} {
			if c.Empty() {
				continue
			}
			if r.cameraButtonTapped(c.Min.X+c.Dx()/2, c.Min.Y+c.Dy()/2) >= 0 {
				t.Errorf("%s: a tap on Talk or the sound was taken for a button", name)
			}
		}
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
