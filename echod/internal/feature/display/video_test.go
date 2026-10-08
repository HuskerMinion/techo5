//go:build !dot && !spot

package display

import (
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
)

// The video page on both panels: the controls where a tap finds them, drawn only over the strip the
// panel blends over the picture, and the pages before the picture and after a failure. With
// SHOW_PREVIEW set, each is written there as the panel would show it: the canvas over a picture
// letterboxed the way the decoder letterboxes one.
func TestTheVideoPage(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 7, 0, 0, time.Local)
	dir := os.Getenv("SHOW_PREVIEW")
	playing := video.State{Phase: video.Playing, ID: 3, Title: "Big Buck Bunny", Host: "192.168.1.20", Frames: true,
		Pos: 83 * time.Second, Dur: 9*time.Minute + 56*time.Second}
	paused := playing
	paused.Phase = video.Paused
	loading := video.State{Phase: video.Loading, ID: 4, Host: "media.example.com"}
	failed := video.State{Phase: video.Idle, Failed: 5, Err: "its video is hevc, which this device does not decode (H.264 and MPEG-4 only)", ErrAt: at}
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"", showWide, showHigh}, {"-show8", show8Wide, show8High}} {
		scenes := map[string]scene{
			"video-picture":  {video: playing, showVideo: true, videoLive: true},
			"video-controls": {video: playing, showVideo: true, videoLive: true, videoControls: true},
			"video-paused":   {video: paused, showVideo: true, videoLive: true, videoControls: true},
			"video-volume":   {video: playing, showVideo: true, videoLive: true, volume: 9, showVolume: true},
			"video-loading":  {video: loading, showVideo: true, videoControls: true},
			"video-failed":   {video: failed, showVideo: true},
			"video-ask":      {showVideoAsk: true, videoAsk: videoAsk{id: 6, from: "192.168.1.30", title: "Holiday 2025.mp4"}},
		}
		for name, s := range scenes {
			s.now, s.phase = at, "idle"
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			r := newRenderer(img)
			r.draw(s)
			if s.videoLive {
				// Only the strip is anything over the picture; the rest of the canvas is clear.
				over := r.videoOver
				for y := 0; y < panel.high; y++ {
					for x := 0; x < panel.wide; x++ {
						if !image.Pt(x, y).In(over) && img.Pix[img.PixOffset(x, y)+3] != 0 {
							t.Fatalf("%s%s: the canvas covers the picture at %d,%d, outside %v", name, panel.name, x, y, over)
						}
					}
				}
				if s.videoControls != !over.Empty() && !s.showVolume {
					t.Errorf("%s%s: controls %v but over %v", name, panel.name, s.videoControls, over)
				}
			}
			if s.videoControls && s.video.Active() {
				play, stop, quieter, louder := r.videoButtons()
				for want, b := range map[videoTap]image.Rectangle{videoTapPlay: play, videoTapStop: stop, videoTapQuieter: quieter, videoTapLouder: louder} {
					mid := b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2))
					if got := r.videoTapped(mid); got != want {
						t.Errorf("%s%s: a tap on %v gave %v, want %v", name, panel.name, mid, got, want)
					}
				}
				if got := r.videoTapped(image.Pt(panel.wide/2, panel.high/3)); got != videoTapNone {
					t.Errorf("%s%s: a tap on the picture gave %v", name, panel.name, got)
				}
			}
			if dir == "" {
				continue
			}
			shown := img
			if s.videoLive {
				shown = overPicture(img)
			}
			f, err := os.Create(filepath.Join(dir, name+panel.name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, shown); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
}

// overPicture is the canvas as the panel shows it over a 16:9 picture with bars at the sides.
func overPicture(canvas *image.RGBA) *image.RGBA {
	b := canvas.Rect
	out := image.NewRGBA(b)
	draw.Draw(out, b, image.Black, image.Point{}, draw.Src)
	w := b.Dy() * 16 / 9
	pic := testPhoto("sky", w, b.Dy())
	x := (b.Dx() - w) / 2
	draw.Draw(out, image.Rect(x, 0, x+w, b.Dy()), pic, image.Point{}, draw.Src)
	draw.Draw(out, b, canvas, image.Point{}, draw.Over)
	return out
}

// What goes over the video, and so pauses it: everything that wants somebody's attention, but not the
// words of a turn left on the screen after it.
func TestWhatCoversAVideo(t *testing.T) {
	for name, tc := range map[string]struct {
		s    scene
		want bool
	}{
		"nothing":   {scene{phase: "idle"}, false},
		"lingering": {scene{phase: "lingering"}, false},
		"listening": {scene{phase: "listening"}, true},
		"replying":  {scene{phase: "replying"}, true},
		"call":      {scene{phase: "idle", call: phone.State{Phase: phone.Ringing}}, true},
		"camera":    {scene{phase: "idle", showCamera: true}, true},
		"setup":     {scene{phase: "idle", setupAsking: true}, true},
		"sheet":     {scene{phase: "idle", showSheet: true}, true},
		"ask":       {scene{phase: "idle", showVideoAsk: true}, true},
		"wifi":      {scene{phase: "idle", showWifi: true}, true},
		"pin":       {scene{phase: "idle", pin: pinView{open: true}}, true},
	} {
		if got := videoCovered(tc.s); got != tc.want {
			t.Errorf("%s: covered %v, want %v", name, got, tc.want)
		}
	}
}

func TestVideoTimes(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "0:00", 65 * time.Second: "1:05", 3725 * time.Second: "1:02:05", -time.Second: "0:00"} {
		if got := videoTime(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
}

// A tap answers the question drawn, and only once it has been up a moment.
func TestATapAnswersOnlyTheDrawnQuestion(t *testing.T) {
	var l askLatch
	now := time.Now()
	if _, ok := l.answerable(now); ok {
		t.Error("answerable with nothing drawn")
	}
	l.drawn(7, now)
	if _, ok := l.answerable(now.Add(askSettle / 2)); ok {
		t.Error("answerable before it settled")
	}
	l.drawn(7, now.Add(askSettle/2)) // drawn again: still the same question, still from now
	if id, ok := l.answerable(now.Add(askSettle)); !ok || id != 7 {
		t.Errorf("answerable %d %v", id, ok)
	}
	l.drawn(8, now.Add(askSettle))
	if _, ok := l.answerable(now.Add(askSettle + time.Millisecond)); ok {
		t.Error("a question that just changed was answerable")
	}
	l.drawn(0, now.Add(2*askSettle))
	if _, ok := l.answerable(now.Add(10 * askSettle)); ok {
		t.Error("answerable after it went")
	}
}
