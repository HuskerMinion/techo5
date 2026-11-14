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
	"github.com/HuskerMinion/techo5/echod/internal/feature/talkback"
)

// Talk on the round camera page: drawn only where the camera has it, tappable where it is drawn, and
// clear of the sound's bar below it. With SPOT_PREVIEW set, each is written there to look at.
func TestTheRoundCameraTalkControl(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	dir := os.Getenv("SPOT_PREVIEW")
	const door = "camera.front_door"
	view := home.CameraView{Entity: door, Name: "Front door", Frame: testPicture(), Until: at.Add(time.Hour)}
	scenes := map[string]roundScene{
		"camera-talk":         {talkOffered: true},
		"camera-talk-sound":   {talkOffered: true, cameraSound: true, cameraSoundLive: true},
		"camera-talk-opening": {talkOffered: true, cameraSound: true, talk: talkback.State{Entity: door, Phase: talkback.Opening}},
		"camera-talk-live":    {talkOffered: true, cameraSound: true, cameraSoundLive: true, talk: talkback.State{Entity: door, Phase: talkback.Talking, Left: 105 * time.Second}},
		"camera-talk-failed": {talkOffered: true, cameraSound: true, talk: talkback.State{Entity: door,
			Error: "the camera's talk-back channel takes MPEG4-GENERIC/16000, which this device cannot send"}},
		"camera-no-talk": {cameraSound: true},
	}
	for name, s := range scenes {
		s.now, s.phase, s.showCamera, s.camera = at, "idle", true, view
		img := image.NewRGBA(image.Rect(0, 0, side, side))
		r := newRoundRenderer(img)
		r.draw(s)
		talk := r.cameraTalkAt
		if !s.talkOffered {
			if !talk.Empty() {
				t.Errorf("%s: Talk drawn for a camera without it", name)
			}
		} else {
			mid := talk.Min.Add(image.Pt(talk.Dx()/2, talk.Dy()/2))
			if !r.cameraTalkTapped(mid.X, mid.Y) || r.cameraSoundTapped(mid.X, mid.Y) {
				t.Errorf("%s: a tap on Talk at %v was not taken for Talk", name, mid)
			}
			if s.cameraSound {
				snd := r.cameraSoundAt
				smid := snd.Min.Add(image.Pt(snd.Dx()/2, snd.Dy()/2))
				if talk.Inset(-cameraBarReach).Overlaps(snd.Inset(-cameraBarReach)) && r.cameraTalkTapped(smid.X, smid.Y) {
					t.Errorf("%s: a tap on the sound's bar was taken for Talk", name)
				}
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
