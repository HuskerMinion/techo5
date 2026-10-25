//go:build !dot && !spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/talkback"
)

// The camera page's action buttons, on each panel: in a row above the strip, clear of Talk, the sound's
// control and a notification's words, each tappable where it is drawn, and not drawn on a view without
// any. With SHOW_PREVIEW set, each is written there to look at.
func TestTheCameraButtons(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	dir := os.Getenv("SHOW_PREVIEW")
	const door = "camera.front_door"
	four := []camButtonFace{{label: "Open gate"}, {label: "Tap again: Unlock", armed: true},
		{label: "No Home Assistant", failed: true}, {label: "Porch light"}}
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"", showWide, showHigh}, {"-show8", show8Wide, show8High}, {"-skylight", skylightWide, skylightHigh}} {
		frame := testPhoto("sky", panel.wide*9/10, panel.high*9/10)
		view := home.CameraView{Entity: door, Name: "Front door", Frame: frame, Until: at.Add(time.Hour)}
		pictured := view
		pictured.Caption = &home.Caption{Title: "Doorbell", Message: "Someone is at the door"}
		scenes := map[string]scene{
			"camera-buttons":         {talkOffered: true, cameraSound: true, camButtons: four[:2], camera: view},
			"camera-buttons-four":    {talkOffered: true, cameraSound: true, camButtons: four, camera: view},
			"camera-buttons-caption": {talkOffered: true, camButtons: four[:1], camera: pictured},
			"camera-buttons-none":    {talkOffered: true, cameraSound: true, camera: view},
		}
		for name, s := range scenes {
			s.now, s.phase, s.showCamera = at, "idle", true
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			r := newRenderer(img)
			r.draw(s)
			if len(r.cameraButtonsAt) != len(s.camButtons) {
				t.Fatalf("%s%s: %d buttons drawn, want %d", name, panel.name, len(r.cameraButtonsAt), len(s.camButtons))
			}
			top := r.h - r.cameraStrip()
			if s.camera.Caption != nil {
				top = r.captionRect(*s.camera.Caption).Min.Y
			}
			for i, b := range r.cameraButtonsAt {
				mid := b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2))
				if got := r.cameraButtonTapped(mid); got != i {
					t.Errorf("%s%s: a tap on button %d at %v found %d", name, panel.name, i, mid, got)
				}
				if r.cameraTalkTapped(mid) || r.cameraSoundTapped(mid) {
					t.Errorf("%s%s: a tap on button %d was taken for Talk or the sound", name, panel.name, i)
				}
				if b.Max.Y > top || b.Min.X < 0 || b.Max.X > r.w {
					t.Errorf("%s%s: button %d at %v, not above %d and on the page", name, panel.name, i, b, top)
				}
				if b.Dy() < r.cameraSoundBox("Talk").Dy() {
					t.Errorf("%s%s: button %d is %d high, less than Talk's %d", name, panel.name, i, b.Dy(), r.cameraSoundBox("Talk").Dy())
				}
				if i > 0 && b.Inset(-r.s(4)).Overlaps(r.cameraButtonsAt[i-1].Inset(-r.s(4))) {
					t.Errorf("%s%s: buttons %d and %d take the same tap", name, panel.name, i-1, i)
				}
			}
			for _, c := range []image.Rectangle{r.cameraTalkAt, r.cameraSoundAt} {
				if c.Empty() {
					continue
				}
				if r.cameraButtonTapped(c.Min.Add(image.Pt(c.Dx()/2, c.Dy()/2))) >= 0 {
					t.Errorf("%s%s: a tap on Talk or the sound was taken for a button", name, panel.name)
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

// A tap on a camera button keeps the view up and does not reach Talk: the first arms the button, the
// second runs it, and a tap off the buttons still takes the view down.
func TestATapOnACameraButtonKeepsTheView(t *testing.T) {
	d := noticeDisplay(t, showWide, showHigh)
	ran := fakeButtonRuns(t, true)
	var talked atomic.Int32
	toggleTalk = func(string) { talked.Add(1) }
	t.Cleanup(func() { toggleTalk = func(entity string) { talkback.Get().Toggle(entity) } })
	gate := config.CameraButton{Label: "Open gate", Service: "script.open_gate"}
	if err := home.SetCameraButtons("camera.gate", []config.CameraButton{gate}); err != nil {
		t.Fatal(err)
	}

	home.Get().ShowCamera("camera.gate", time.Minute)
	v, _ := home.Get().Camera()
	redraw := func() {
		now := time.Now()
		d.r.draw(scene{now: now, phase: "idle", showCamera: true, camera: v, talkOffered: true,
			camButtons: d.camButtons.faces(v.Entity, cameraButtonsFor(v), now)})
	}
	redraw()
	if len(d.r.cameraButtonsAt) != 1 {
		t.Fatalf("%d buttons drawn, want the gate's", len(d.r.cameraButtonsAt))
	}
	b := d.r.cameraButtonsAt[0]
	mid := b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2))

	tapAt(d, mid)
	noRun(t, ran, "the first tap")
	redraw()
	if f := d.camButtons.faces(v.Entity, cameraButtonsFor(v), time.Now()); !f[0].armed {
		t.Errorf("after the first tap the button is %+v, want it armed", f[0])
	}
	b = d.r.cameraButtonsAt[0] // drawn wider, with the words that ask for the second tap
	tapAt(d, b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2)))
	if got := ranOne(t, ran, "the second tap"); got != gate {
		t.Errorf("ran %+v, want the gate's", got)
	}
	time.Sleep(100 * time.Millisecond) // a close, if one were wrongly started, has had its turn
	if _, up := home.Get().Camera(); !up {
		t.Fatal("a tap on a camera button closed the camera view")
	}
	if talked.Load() != 0 {
		t.Error("a tap on a camera button reached Talk")
	}

	tapAt(d, image.Pt(showWide/2, showHigh/3))
	within(t, "the view coming down", func() bool { _, up := home.Get().Camera(); return !up })
}

// fairWidth: the narrow keep theirs, the widest share what is left.
func TestFairWidth(t *testing.T) {
	if got := fairWidth([]int{100, 300, 100}, 400); got != 200 {
		t.Errorf("fairWidth = %d, want 200", got)
	}
	if got := fairWidth([]int{100, 300, 300}, 400); got != 150 {
		t.Errorf("fairWidth = %d, want 150", got)
	}
}
