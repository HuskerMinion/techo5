//go:build spot

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

	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
)

// The Spot's video face: the controls inside the circle and where a tap finds them, nothing but the
// strip over the picture, and the faces before the picture, after a failure and for the question.
// With SPOT_PREVIEW set, each is written there as the round panel shows it.
func TestTheSpotVideoFace(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	dir := os.Getenv("SPOT_PREVIEW")
	playing := video.State{Phase: video.Playing, ID: 3, Title: "Big Buck Bunny", Frames: true, Pos: 83 * time.Second, Dur: 9*time.Minute + 56*time.Second}
	paused := playing
	paused.Phase = video.Paused
	scenes := map[string]roundScene{
		"video-spot-picture":  {video: playing, showVideo: true, videoLive: true},
		"video-spot-controls": {video: playing, showVideo: true, videoLive: true, videoControls: true},
		"video-spot-paused":   {video: paused, showVideo: true, videoLive: true, videoControls: true},
		"video-spot-volume":   {video: playing, showVideo: true, videoLive: true, videoControls: true, showVolume: true, volume: 9},
		"video-spot-loading":  {video: video.State{Phase: video.Loading, ID: 4, Host: "media.example.com"}, showVideo: true, videoControls: true},
		"video-spot-failed":   {video: video.State{Failed: 5, Err: "Server returned 404 Not Found", ErrAt: at}, showVideo: true},
		"video-spot-ask":      {showVideoAsk: true, videoAsk: videoAsk{id: 6, from: "192.168.1.30", title: "Holiday 2025.mp4"}},
	}
	for name, s := range scenes {
		s.now, s.phase = at, "idle"
		img := image.NewRGBA(image.Rect(0, 0, side, side))
		r := newRoundRenderer(img)
		r.draw(s)
		if s.videoLive {
			for y := 0; y < side; y++ {
				for x := 0; x < side; x++ {
					if !image.Pt(x, y).In(r.videoOver) && img.Pix[img.PixOffset(x, y)+3] != 0 {
						t.Fatalf("%s: the canvas covers the picture at %d,%d", name, x, y)
					}
				}
			}
		}
		if s.videoControls && s.video.Active() {
			for i, b := range r.videoButtonsSpot() {
				// Every corner of every button inside the circle, with room to spare.
				for _, c := range []image.Point{b.Min, {b.Max.X, b.Min.Y}, {b.Min.X, b.Max.Y}, b.Max} {
					dx, dy := c.X-center, c.Y-center
					if dx*dx+dy*dy > (center-8)*(center-8) {
						t.Errorf("%s: button %d's corner %v is under the bezel", name, i, c)
					}
				}
				mid := b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2))
				if got := r.videoTappedSpot(mid); got != videoTap(i+1) {
					t.Errorf("%s: a tap on button %d gave %v", name, i, got)
				}
			}
			if got := r.videoTappedSpot(image.Pt(center, 150)); got != videoTapNone {
				t.Errorf("%s: a tap on the picture gave %v", name, got)
			}
		}
		if dir == "" {
			continue
		}
		shown := img
		if s.videoLive {
			fit := video.FitIn(video.Info{Width: 1280, Height: 720}, video.Screen{W: side, H: side})
			shown = roundPanel(overSpotPicture(img, image.Rect(fit.X, fit.Y, fit.X+fit.W, fit.Y+fit.H)))
		} else {
			shown = roundPanel(img)
		}
		writePNG(t, filepath.Join(dir, name+".png"), shown)
	}
}

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// overSpotPicture is the canvas as the panel shows it over a picture placed at at.
func overSpotPicture(canvas *image.RGBA, at image.Rectangle) *image.RGBA {
	out := image.NewRGBA(canvas.Rect)
	draw.Draw(out, out.Rect, image.Black, image.Point{}, draw.Src)
	draw.Draw(out, at, testPhoto("sky", at.Dx(), at.Dy()), image.Point{}, draw.Src)
	draw.Draw(out, out.Rect, canvas, image.Point{}, draw.Over)
	return out
}

// roundPanel is what the round glass lets through: outside the circle is the bezel.
func roundPanel(img *image.RGBA) *image.RGBA {
	out := image.NewRGBA(img.Rect)
	draw.Draw(out, out.Rect, img, image.Point{}, draw.Src)
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			if dx, dy := x-center, y-center; dx*dx+dy*dy > center*center {
				out.SetRGBA(x, y, color.RGBA{40, 40, 40, 255})
			}
		}
	}
	return out
}

// What covers the Spot's video, and so pauses it.
func TestWhatCoversAVideoOnTheSpot(t *testing.T) {
	for name, tc := range map[string]struct {
		s    roundScene
		want bool
	}{
		"nothing":   {roundScene{phase: "idle"}, false},
		"lingering": {roundScene{phase: "lingering"}, false},
		"listening": {roundScene{phase: "listening"}, true},
		"call":      {roundScene{phase: "idle", call: phone.State{Phase: phone.Ringing}}, true},
		"menu":      {roundScene{phase: "idle", menuOpen: true}, true},
		"camera":    {roundScene{phase: "idle", showCamera: true}, true},
		"ask":       {roundScene{phase: "idle", showVideoAsk: true}, true},
		"reminder":  {roundScene{phase: "idle", showReminder: true}, true},
	} {
		if got := videoCoveredSpot(tc.s); got != tc.want {
			t.Errorf("%s: covered %v, want %v", name, got, tc.want)
		}
	}
}
