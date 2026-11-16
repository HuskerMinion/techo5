//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// stalePicture is a picture of w by h in one color no page element has, to see what is drawn over it.
func stalePicture(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Rect, image.NewUniform(color.RGBA{0x30, 0x58, 0x74, 0xff}), image.Point{}, draw.Src)
	return img
}

// streamedPicture stands a picture in for the streamed dashboard's for the test.
func streamedPicture(t *testing.T) {
	t.Helper()
	was := drawStream
	drawStream = func(dst *image.RGBA) bool {
		draw.Draw(dst, dst.Rect, stalePicture(dst.Rect.Dx(), dst.Rect.Dy()), image.Point{}, draw.Src)
		return true
	}
	t.Cleanup(func() { drawStream = was })
}

// changedIn counts the pixels of area that differ between a and b.
func changedIn(a, b *image.RGBA, area image.Rectangle) int {
	n := 0
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			if a.RGBAAt(x, y) != b.RGBAAt(x, y) {
				n++
			}
		}
	}
	return n
}

// A streamed dashboard whose connection has been gone a while says its picture is not live, in the
// corner at the foot and nowhere near the page's own header; one that just dropped, or is live, does
// not.
func TestAStaleDashboardSaysSo(t *testing.T) {
	streamedPicture(t)
	at := time.Date(2026, 10, 20, 9, 30, 0, 0, time.Local)
	if dashStale(dashboard.View{Ready: true}, at) {
		t.Error("a live stream is stale")
	}
	if dashStale(dashboard.View{Ready: true, Lost: at.Add(-time.Second)}, at) {
		t.Error("a stream that dropped a second ago is already stale")
	}
	if dashStale(dashboard.View{Lost: at.Add(-time.Minute)}, at) {
		t.Error("a stream with no picture up is stale")
	}
	if !dashStale(dashboard.View{Ready: true, Lost: at.Add(-dashLostGrace)}, at) {
		t.Errorf("a stream lost for %v is not stale", dashLostGrace)
	}
	for _, panel := range showPanels() {
		frame := func(v dashboard.View) (*image.RGBA, *renderer) {
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			r := newRenderer(img)
			r.draw(scene{now: at, phase: "idle", showDash: true, dashMode: config.DashboardStreamed, dash: v})
			return img, r
		}
		live, r := frame(dashboard.View{Ready: true})
		fresh, _ := frame(dashboard.View{Ready: true, Lost: at.Add(-time.Second)})
		stale, _ := frame(dashboard.View{Ready: true, Lost: at.Add(-time.Minute)})
		if n := changedIn(live, fresh, live.Rect); n != 0 {
			t.Errorf("%s: a stream that just dropped changed %d pixels of the page", panel.name, n)
		}
		corner := image.Rect(panel.wide/2, panel.high-r.s(60), panel.wide, panel.high)
		if changedIn(live, stale, corner) == 0 || inkIn(stale, corner, amber) == 0 {
			t.Errorf("%s: a stale stream has no chip in the corner at the foot", panel.name)
		}
		rest := image.Rect(0, 0, panel.wide, panel.high-r.s(60))
		if n := changedIn(live, stale, rest); n != 0 {
			t.Errorf("%s: the chip changed %d pixels above the foot", panel.name, n)
		}
		// With what went wrong along the foot, the chip sits over that line and not on it.
		problem := dashboard.View{Ready: true, Problem: "The dashboard server did not answer.", Lost: at.Add(-time.Minute)}
		said, _ := frame(problem)
		problem.Lost = time.Time{}
		plain, _ := frame(problem)
		strip := image.Rect(0, panel.high-r.s(36), panel.wide, panel.high)
		if n := changedIn(plain, said, strip); n != 0 {
			t.Errorf("%s: the chip is over %d pixels of the line at the foot", panel.name, n)
		}
		if changedIn(plain, said, image.Rect(panel.wide/2, panel.high-r.s(90), panel.wide, strip.Min.Y)) == 0 {
			t.Errorf("%s: no chip over the line at the foot", panel.name)
		}
	}
}

// A camera whose frame failed after one had come keeps the last picture and says it is not live; the
// next frame takes the chip away, and a view with no picture yet says why in words as before.
func TestAStaleCameraSaysSo(t *testing.T) {
	at := time.Date(2026, 10, 20, 9, 30, 0, 0, time.Local)
	for _, panel := range showPanels() {
		pic := stalePicture(panel.wide, panel.high)
		frame := func(v home.CameraView) (*image.RGBA, *renderer) {
			v.Entity, v.Name, v.Until = "camera.front_door", "Front door", at.Add(30*time.Second)
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			r := newRenderer(img)
			r.draw(scene{now: at, phase: "idle", showCamera: true, camera: v})
			return img, r
		}
		live, r := frame(home.CameraView{Frame: pic})
		stale, _ := frame(home.CameraView{Frame: pic, Error: "the camera did not answer"})
		under := image.Rect(0, r.s(44), panel.wide/2, r.s(110))
		if changedIn(live, stale, under) == 0 || inkIn(stale, under, amber) == 0 {
			t.Errorf("%s: a camera whose frame failed has no chip under its name", panel.name)
		}
		if n := changedIn(live, stale, image.Rect(0, r.s(110), panel.wide, panel.high)); n != 0 {
			t.Errorf("%s: the chip changed %d pixels of the rest of the view", panel.name, n)
		}
		again, _ := frame(home.CameraView{Frame: pic})
		if n := changedIn(live, again, live.Rect); n != 0 {
			t.Errorf("%s: the next frame left %d pixels of the chip", panel.name, n)
		}
		waiting, _ := frame(home.CameraView{})
		failed, _ := frame(home.CameraView{Error: "the camera did not answer"})
		if inkIn(failed, under, amber) != 0 || changedIn(waiting, failed, failed.Rect) == 0 {
			t.Errorf("%s: a view with no picture yet has the chip, or no words", panel.name)
		}
	}
}

// With SHOW_PREVIEW set, the two stale pictures are written there on every panel to look at.
func TestStalePreviews(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	if dir == "" {
		t.Skip("SHOW_PREVIEW is not set")
	}
	streamedPicture(t)
	at := time.Date(2026, 10, 20, 9, 30, 0, 0, time.Local)
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"", showWide, showHigh}, {"-show8", show8Wide, show8High}, {"-skylight", skylightWide, skylightHigh}} {
		lost := dashboard.View{Ready: true, Lost: at.Add(-time.Minute)}
		for name, s := range map[string]scene{
			"stale-dashboard": {showDash: true, dashMode: config.DashboardStreamed, dash: lost},
			"stale-dashboard-problem": {showDash: true, dashMode: config.DashboardStreamed,
				dash: dashboard.View{Ready: true, Lost: lost.Lost, Problem: "The dashboard server did not accept this device's key."}},
			"stale-camera": {showCamera: true, camera: home.CameraView{Entity: "camera.front_door", Name: "Front door",
				Until: at.Add(30 * time.Second), Frame: stalePicture(panel.wide, panel.high), Error: "the camera did not answer"}},
			"stale-camera-captioned": {showCamera: true, camera: home.CameraView{Entity: "camera.front_door", Name: "Front door",
				Until: at.Add(30 * time.Second), Frame: stalePicture(panel.wide, panel.high), Error: "the camera did not answer",
				Caption: &home.Caption{ID: "1", Title: "Doorbell", Message: "Someone is at the front door"}}},
		} {
			s.now, s.phase = at, "idle"
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			newRenderer(img).draw(s)
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
