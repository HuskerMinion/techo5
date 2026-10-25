//go:build !dot && !spot

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

// Talk on the camera page, on both panels: drawn only where the camera has it, tappable where it is
// drawn and nowhere else, clear of the sound's control and of the hint, and the hint cut short rather
// than run under it. With SHOW_PREVIEW set, each is written there to look at.
func TestTheCameraTalkControl(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	dir := os.Getenv("SHOW_PREVIEW")
	const door = "camera.front_door"
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"", showWide, showHigh}, {"-show8", show8Wide, show8High}, {"-skylight", skylightWide, skylightHigh}} {
		frame := testPhoto("sky", panel.wide*9/10, panel.high*9/10)
		view := home.CameraView{Entity: door, Name: "Front door", Frame: frame, Until: at.Add(time.Hour)}
		scenes := map[string]scene{
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
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			r := newRenderer(img)
			r.draw(s)
			talk := r.cameraTalkAt
			if !s.talkOffered {
				if !talk.Empty() {
					t.Errorf("%s%s: Talk drawn for a camera without it", name, panel.name)
				}
			} else {
				mid := talk.Min.Add(image.Pt(talk.Dx()/2, talk.Dy()/2))
				if !r.cameraTalkTapped(mid) || r.cameraSoundTapped(mid) {
					t.Errorf("%s%s: a tap on Talk at %v was not taken for Talk", name, panel.name, mid)
				}
				if s.cameraSound && talk.Overlaps(r.cameraSoundAt) {
					t.Errorf("%s%s: Talk %v over the sound's control %v", name, panel.name, talk, r.cameraSoundAt)
				}
				// A finger just under the bar, at the screen's very edge, is on it too.
				if edge := image.Pt(mid.X, r.h-1); !r.cameraTalkTapped(edge) {
					t.Errorf("%s%s: a tap at the bottom edge under Talk, %v, missed it", name, panel.name, edge)
				}
				if r.cameraTalkTapped(image.Pt(r.margin, r.h-11)) {
					t.Errorf("%s%s: a tap on the hint was taken for Talk", name, panel.name)
				}
				if talk.Max.X > r.w-r.margin || talk.Min.X < r.w/3 {
					t.Errorf("%s%s: Talk at %v, not at the strip's end", name, panel.name, talk)
				}
			}
			if dir == "" {
				continue
			}
			f, err := os.Create(filepath.Join(dir, name+panel.name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, img); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
}

// A tap on Talk while the next frame is being drawn still finds Talk: the controls are published when
// a frame is done, never cleared while it draws, or the tap would close the view instead.
func TestTalkIsTappableWhileAFrameDraws(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	s := scene{now: at, phase: "idle", showCamera: true, talkOffered: true, cameraSound: true,
		camera: home.CameraView{Entity: "camera.front_door", Name: "Front door", Until: at.Add(time.Hour)}}
	r.draw(s)
	b := r.cameraTalkAt
	mid := b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2))
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 30; i++ {
			r.draw(s)
		}
	}()
	for {
		select {
		case <-done:
			return
		default:
		}
		if !r.cameraTalkTapped(mid) {
			t.Fatal("a tap on Talk while a frame was drawing found nothing there")
		}
	}
}

// The volume bar covers the camera page's controls: under it, neither Talk nor the sound control
// takes a tap.
func TestNothingUnderTheVolumeBarIsTapped(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	s := scene{now: at, phase: "idle", showCamera: true, talkOffered: true, cameraSound: true,
		camera: home.CameraView{Entity: "camera.front_door", Name: "Front door", Until: at.Add(time.Hour)}}
	r.draw(s)
	talk, sound := r.cameraTalkAt, r.cameraSoundAt
	s.showVolume = true
	r.draw(s)
	for _, b := range []image.Rectangle{talk, sound} {
		mid := b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2))
		if r.cameraTalkTapped(mid) || r.cameraSoundTapped(mid) {
			t.Errorf("a tap at %v under the volume bar was taken", mid)
		}
	}
}
