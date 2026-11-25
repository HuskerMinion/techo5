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
	"github.com/HuskerMinion/techo5/echod/internal/lib/mdi"
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
	filled, stretched := playing, playing
	filled.Aspect, stretched.Aspect = video.AspectFill, video.AspectStretch
	loading := video.State{Phase: video.Loading, ID: 4, Host: "media.example.com"}
	failed := video.State{Phase: video.Idle, Failed: 5, Err: "its video is hevc, which this device does not decode (H.264 and MPEG-4 only)", ErrAt: at}
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"", showWide, showHigh}, {"-show8", show8Wide, show8High}, {"-skylight", skylightWide, skylightHigh}} {
		scenes := map[string]scene{
			"video-picture":  {video: playing, showVideo: true, videoLive: true},
			"video-controls": {video: playing, showVideo: true, videoLive: true, videoControls: true},
			"video-skip":     {video: playing, showVideo: true, videoLive: true, videoControls: true, videoSkip: true},
			"video-paused":   {video: paused, showVideo: true, videoLive: true, videoControls: true},
			"video-volume":   {video: playing, showVideo: true, videoLive: true, volume: 9, showVolume: true},
			"video-loading":  {video: loading, showVideo: true, videoControls: true},
			"video-failed":   {video: failed, showVideo: true},
			"video-ask":      {showVideoAsk: true, videoAsk: videoAsk{id: 6, from: "192.168.1.30", title: "Holiday 2025.mp4"}},
			// A video somebody can move through: the bar over the buttons, and a finger choosing a place.
			"video-seek": {video: playing, showVideo: true, videoLive: true, videoControls: true,
				videoSeek: seekView{route: seekVideo, id: 3, dur: playing.Dur}},
			"video-seek-drag": {video: playing, showVideo: true, videoLive: true, videoControls: true, videoSkip: true,
				videoSeek: seekView{route: seekDLNA, id: 3, dur: playing.Dur, shown: true, to: 6 * time.Minute}},
			// The button for the way the picture takes the screen (#733), its icon the way it is now; and
			// with everything else the page can have, so that nothing runs into anything.
			"video-fit": {video: playing, showVideo: true, videoLive: true, videoControls: true, videoFit: true,
				videoSeek: seekView{route: seekVideo, id: 3, dur: playing.Dur}},
			"video-fit-fill": {video: filled, showVideo: true, videoLive: true, videoControls: true, videoFit: true,
				videoSeek: seekView{route: seekVideo, id: 3, dur: playing.Dur}},
			"video-fit-stretch": {video: stretched, showVideo: true, videoLive: true, videoControls: true, videoFit: true,
				videoSeek: seekView{route: seekVideo, id: 3, dur: playing.Dur}},
			"video-fit-skip": {video: playing, showVideo: true, videoLive: true, videoControls: true, videoSkip: true, videoFit: true,
				videoSeek: seekView{route: seekDLNA, id: 3, dur: playing.Dur}},
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
				// Back and forward only for a DLNA video whose controller listens (videoSkip): otherwise
				// where they would be is nothing, not a dead button.
				back, next := r.videoSkipButtons()
				for want, b := range map[videoTap]image.Rectangle{videoTapBack: back, videoTapNext: next} {
					if !s.videoSkip {
						want = videoTapNone
					}
					mid := b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2))
					if got := r.videoTapped(mid); got != want {
						t.Errorf("%s%s: a tap on %v gave %v, want %v (skip %v)", name, panel.name, mid, got, want, s.videoSkip)
					}
				}
				// The button for the way the picture takes the screen only where the player can start the
				// video again where it is (videoFit), before quieter, the gap of the others from it.
				fit := r.videoFitButton()
				want := videoTapNone
				if s.videoFit {
					want = videoTapFit
				}
				if got := r.videoTapped(fit.Min.Add(image.Pt(fit.Dx()/2, fit.Dy()/2))); got != want {
					t.Errorf("%s%s: a tap on the fit button gave %v, want %v", name, panel.name, got, want)
				}
				if fit.Max.X+r.s(videoGap) != quieter.Min.X || fit.Min.Y != quieter.Min.Y || fit.Size() != quieter.Size() {
					t.Errorf("%s%s: the fit button %v is not beside quieter %v", name, panel.name, fit, quieter)
				}
				firstRight := quieter
				if s.videoFit {
					firstRight = fit
				}
				lastLeft := stop
				if s.videoSkip {
					lastLeft = next
				}
				// Room between the groups for the name and the time, and no zone a finger's reach into
				// another's middle.
				if lastLeft.Max.X+r.s(28)+r.s(56) >= firstRight.Min.X-r.s(28) {
					t.Errorf("%s%s: %v runs into %v", name, panel.name, lastLeft, firstRight)
				}
				for _, a := range r.videoZones {
					for _, b := range r.videoZones {
						if a.tap != b.tap && a.r.Inset(-r.s(8)).Overlaps(b.r) {
							t.Errorf("%s%s: %v's reach takes %v", name, panel.name, a.tap, b.tap)
						}
					}
				}
				// The bar to seek on only for a video somebody can move through, inside what goes over
				// the picture, and never where a finger meant for a button lands.
				z := r.seekBars.zone()
				if (z.view.route != seekNone) != s.videoSeek.tappable() {
					t.Errorf("%s%s: bar %+v for a video whose seek is %+v", name, panel.name, z.view, s.videoSeek)
				}
				if s.videoSeek.tappable() {
					if !z.bar.In(r.videoOver) {
						t.Errorf("%s%s: the bar %v is outside the strip %v", name, panel.name, z.bar, r.videoOver)
					}
					for _, b := range append([]image.Rectangle{play, stop, quieter, louder}, back, next, fit) {
						if z.reach.Overlaps(b.Inset(-r.s(8))) {
							t.Errorf("%s%s: the bar's reach %v takes a button's %v", name, panel.name, z.reach, b)
						}
					}
					if got := z.at(z.bar.Min.X + z.bar.Dx()/2); got != s.videoSeek.dur/2 {
						t.Errorf("%s%s: the bar's middle is %v of %v", name, panel.name, got, s.videoSeek.dur)
					}
				}
			}
			if dir == "" {
				continue
			}
			shown := img
			if s.videoLive {
				shown = overPicture(img, s.video.Aspect)
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

// overPicture is the canvas as the panel shows it over a 16:9 picture taken the way a says: with bars
// at the sides, filling the screen with its top and foot cut off, or pulled out to the screen's shape.
func overPicture(canvas *image.RGBA, a video.Aspect) *image.RGBA {
	b := canvas.Rect
	out := image.NewRGBA(b)
	draw.Draw(out, b, image.Black, image.Point{}, draw.Src)
	switch a {
	case video.AspectFill:
		w := max(b.Dx(), b.Dy()*16/9)
		h := w * 9 / 16
		draw.Draw(out, b, testPhoto("sky", w, h), image.Pt((w-b.Dx())/2, (h-b.Dy())/2), draw.Src)
	case video.AspectStretch:
		draw.Draw(out, b, testPhoto("sky", b.Dx(), b.Dy()), image.Point{}, draw.Src)
	default:
		w := b.Dy() * 16 / 9
		pic := testPhoto("sky", w, b.Dy())
		x := (b.Dx() - w) / 2
		draw.Draw(out, image.Rect(x, 0, x+w, b.Dy()), pic, image.Point{}, draw.Src)
	}
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
		// A cue: its light, and its captions while they stay, which the video would cover.
		"cue":            {scene{phase: "idle", cue: "thinking"}, true},
		"cue captions":   {scene{phase: "idle", cue: "lingering", reply: "It's noon."}, true},
		"cue, no words":  {scene{phase: "idle", cue: "lingering"}, false},
		"classic answer": {scene{phase: "lingering", reply: "It's noon."}, false},
	} {
		if got := videoCovered(tc.s); got != tc.want {
			t.Errorf("%s: covered %v, want %v", name, got, tc.want)
		}
	}
}

// The button for the way the picture takes the screen is offered only once the player knows it can
// start the video again where it is, and its icon is the way the picture is now.
func TestTheFitButtonIsOfferedWhereItWorks(t *testing.T) {
	savedFittable, savedRefit := videoFittable, videoRefitTo
	t.Cleanup(func() { videoFittable, videoRefitTo = savedFittable, savedRefit })
	ok, known := true, true
	videoFittable = func(uint64) (bool, bool) { return ok, known }
	playing := video.State{Phase: video.Playing, ID: 3, Frames: true}
	if !videoFittableNow(playing) {
		t.Error("not offered for a video the player can start again")
	}
	for name, st := range map[string]video.State{"idle": {}, "asking": {Phase: video.Asking, ID: 4}} {
		if videoFittableNow(st) {
			t.Errorf("offered while %s", name)
		}
	}
	ok = false
	if videoFittableNow(playing) {
		t.Error("offered for a live stream")
	}
	ok, known = true, false
	if videoFittableNow(playing) {
		t.Error("offered before the player knows")
	}
	var asked []uint64
	videoRefitTo = func(id uint64) (video.Aspect, error) { asked = append(asked, id); return video.AspectFill, nil }
	videoRefit(3)
	if len(asked) != 1 || asked[0] != 3 {
		t.Errorf("refit asked for %v", asked)
	}
	icons := map[string]bool{}
	for _, a := range []video.Aspect{video.AspectFit, video.AspectFill, video.AspectStretch} {
		icon := videoFitIcon(a)
		if _, found := mdi.Rune(icon); !found {
			t.Errorf("%v: no icon %q in the font", a, icon)
		}
		icons[icon] = true
	}
	if len(icons) != 3 {
		t.Errorf("the three ways share icons: %v", icons)
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
