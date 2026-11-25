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
	filled, stretched := playing, playing
	filled.Aspect, stretched.Aspect = video.AspectFill, video.AspectStretch
	scenes := map[string]roundScene{
		"video-spot-picture":  {video: playing, showVideo: true, videoLive: true},
		"video-spot-controls": {video: playing, showVideo: true, videoLive: true, videoControls: true},
		"video-spot-paused":   {video: paused, showVideo: true, videoLive: true, videoControls: true},
		"video-spot-volume":   {video: playing, showVideo: true, videoLive: true, videoControls: true, showVolume: true, volume: 9},
		"video-spot-loading":  {video: video.State{Phase: video.Loading, ID: 4, Host: "media.example.com"}, showVideo: true, videoControls: true},
		"video-spot-failed":   {video: video.State{Failed: 5, Err: "Server returned 404 Not Found", ErrAt: at}, showVideo: true},
		"video-spot-ask":      {showVideoAsk: true, videoAsk: videoAsk{id: 6, from: "192.168.1.30", title: "Holiday 2025.mp4"}},
		// A video somebody can move through: the bar above the title, and a finger choosing a place.
		"video-spot-seek": {video: playing, showVideo: true, videoLive: true, videoControls: true,
			videoSeek: seekView{route: seekVideo, id: 3, dur: playing.Dur}},
		"video-spot-seek-drag": {video: playing, showVideo: true, videoLive: true, videoControls: true,
			videoSeek: seekView{route: seekVideo, id: 3, dur: playing.Dur, shown: true, to: 6 * time.Minute}},
		// The button for the way the picture takes the screen (#733), five buttons on the chord, its icon
		// the way it is now.
		"video-spot-fit": {video: playing, showVideo: true, videoLive: true, videoControls: true, videoFit: true,
			videoSeek: seekView{route: seekVideo, id: 3, dur: playing.Dur}},
		"video-spot-fit-fill": {video: filled, showVideo: true, videoLive: true, videoControls: true, videoFit: true,
			videoSeek: seekView{route: seekVideo, id: 3, dur: playing.Dur}},
		"video-spot-fit-stretch": {video: stretched, showVideo: true, videoLive: true, videoControls: true, videoFit: true,
			videoSeek: seekView{route: seekVideo, id: 3, dur: playing.Dur}},
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
			buttons := r.videoButtonsSpot(s.videoFit)
			want := []videoTap{videoTapPlay, videoTapStop, videoTapQuieter, videoTapLouder}
			if s.videoFit {
				want = []videoTap{videoTapPlay, videoTapStop, videoTapFit, videoTapQuieter, videoTapLouder}
			}
			if len(buttons) != len(want) {
				t.Fatalf("%s: %d buttons, want %d", name, len(buttons), len(want))
			}
			for i, z := range buttons {
				b := z.r
				// Every corner of every button inside the circle, with room to spare.
				for _, c := range []image.Point{b.Min, {b.Max.X, b.Min.Y}, {b.Min.X, b.Max.Y}, b.Max} {
					dx, dy := c.X-center, c.Y-center
					if dx*dx+dy*dy > (center-8)*(center-8) {
						t.Errorf("%s: button %d's corner %v is under the bezel", name, i, c)
					}
				}
				mid := b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2))
				if got := r.videoTappedSpot(mid); got != want[i] {
					t.Errorf("%s: a tap on button %d gave %v, want %v", name, i, got, want[i])
				}
				// No button's reach takes another's middle, nor its icon's square.
				for j, o := range buttons {
					icon := image.Rect(0, 0, videoIconSpot, videoIconSpot).Add(o.r.Min.Add(image.Pt((o.r.Dx()-videoIconSpot)/2, (o.r.Dy()-videoIconSpot)/2)))
					if j != i && b.Inset(-6).Overlaps(icon) {
						t.Errorf("%s: button %d's reach takes button %d's icon", name, i, j)
					}
				}
			}
			if !s.videoFit {
				// Where the fit button would be, with five, is another button or nothing: never the fit.
				for _, z := range r.videoButtonsSpot(true) {
					if got := r.videoTappedSpot(z.r.Min.Add(image.Pt(z.r.Dx()/2, z.r.Dy()/2))); got == videoTapFit {
						t.Errorf("%s: a fit button with none drawn", name)
					}
				}
			}
			if got := r.videoTappedSpot(image.Pt(center, 150)); got != videoTapNone {
				t.Errorf("%s: a tap on the picture gave %v", name, got)
			}
			// The bar to seek on only for a video somebody can move through: inside the circle and the
			// strip, and clear of where a finger meant for a button lands.
			z := r.seekBars.zone()
			if (z.view.route != seekNone) != s.videoSeek.tappable() {
				t.Errorf("%s: bar %+v for a video whose seek is %+v", name, z.view, s.videoSeek)
			}
			if s.videoSeek.tappable() {
				for _, c := range []image.Point{z.reach.Min, {z.reach.Max.X, z.reach.Min.Y}, {z.reach.Min.X, z.reach.Max.Y}, z.reach.Max} {
					dx, dy := c.X-center, c.Y-center
					if dx*dx+dy*dy > (center-8)*(center-8) {
						t.Errorf("%s: the bar's corner %v is under the bezel", name, c)
					}
				}
				if !z.bar.In(r.videoOver) {
					t.Errorf("%s: the bar %v is outside the strip %v", name, z.bar, r.videoOver)
				}
				for _, b := range buttons {
					if z.reach.Overlaps(b.r.Inset(-6)) {
						t.Errorf("%s: the bar's reach %v takes a button's %v", name, z.reach, b.r)
					}
				}
			}
		}
		if dir == "" {
			continue
		}
		shown := img
		if s.videoLive {
			fit := video.FitIn(video.Info{Width: 1280, Height: 720}, video.Screen{W: side, H: side})
			at := image.Rect(fit.X, fit.Y, fit.X+fit.W, fit.Y+fit.H)
			switch s.video.Aspect {
			case video.AspectFill:
				// The 16:9 picture as tall as the square, its sides cut off by the panel's edge.
				w := side * 16 / 9
				at = image.Rect((side-w)/2, 0, (side-w)/2+w, side)
			case video.AspectStretch:
				at = image.Rect(0, 0, side, side)
			}
			shown = roundPanel(overSpotPicture(img, at))
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
