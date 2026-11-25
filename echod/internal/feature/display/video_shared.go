//go:build !dot

package display

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"log/slog"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dlna"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/screen"
)

// What the Show's video page (video.go) and the Spot's (video_spot.go) share: the timings, the
// controls a tap can land on, and the painting of the frames, which is the same on both panels.

// videoControls is how long the controls stay up after a tap.
const videoControls = 4 * time.Second

// videoRecheck is how often the frame loop looks again at everything else while a video plays: a
// call or a ring coming over it is noticed within this, or at once when it wakes the loop.
const videoRecheck = 500 * time.Millisecond

// videoFailShown is how long the page says a video could not be played.
const videoFailShown = 6 * time.Second

// videoTap is a tap on the video page's controls.
type videoTap int

const (
	videoTapNone videoTap = iota
	videoTapPlay
	videoTapStop
	videoTapQuieter
	videoTapLouder
	// The back and forward a DLNA video has while its controller listens (videoSkip), drawn after
	// the rest. The Spot's video screen has no skip buttons, so its build never names them.
	//lint:ignore U1000 used by the Show's video screen (video.go), not the Spot's
	videoTapBack
	//lint:ignore U1000 used by the Show's video screen (video.go), not the Spot's
	videoTapNext
	// The button that takes the picture round its ways of taking the screen (videoRefit), for a video
	// the player can start again where it is (video.Refittable).
	videoTapFit
)

// videoZone is a control on the video page as last drawn, and what a tap on it is.
type videoZone struct {
	r   image.Rectangle
	tap videoTap
}

// videoZoneAt is the control a tap at at landed on, a finger reach (pixels) wider than its edges.
func videoZoneAt(zones []videoZone, at image.Point, reach int) videoTap {
	for _, z := range zones {
		if at.In(z.r.Inset(-reach)) {
			return z.tap
		}
	}
	return videoTapNone
}

// The player's say on the way the picture takes the screen; tests put their own in.
var (
	videoFittable = video.Refittable
	videoRefitTo  = video.Refit
)

// videoFittableNow is whether the video in st gets the button for its way of taking the screen: a
// video the player can start again where it is, once that is known.
func videoFittableNow(st video.State) bool {
	if !st.Active() || st.Phase == video.Asking {
		return false
	}
	ok, known := videoFittable(st.ID)
	return ok && known
}

// videoRefit takes the video on to its next way of taking the screen. It is not waited for: the
// player stops and starts the decoder.
func videoRefit(id uint64) {
	a, err := videoRefitTo(id)
	if err != nil {
		slog.Info("screen: the video's picture was not changed", "err", err)
		return
	}
	slog.Info("screen: the video's picture", "aspect", a)
}

// videoFitIcon is the button's icon: the way the picture takes the screen now, so a glance at it says
// which it is with no words to translate. Whole with bars is a screen with the picture fitted in it,
// filling is a crop, stretching is a picture pulled out to the page's edges.
func videoFitIcon(a video.Aspect) string {
	switch a {
	case video.AspectFill:
		return "crop"
	case video.AspectStretch:
		return "stretch-to-page-outline"
	}
	return "fit-to-screen-outline"
}

// videoBarUp is whether a finger on the video page may be its bar's (seekGesture): while the controls
// it is drawn with are up (as a tap on the page finds them, videoGesture), or while a finger that came
// down on it is still on it. With the controls away, the bar the last frame drew is not there to the
// eye, and a tap that brings the controls back must not move the video as well.
func (d *Display) videoBarUp(st video.State) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	up := time.Now().Before(d.videoUntil) || st.Phase == video.Paused || st.Phase == video.Loading || !st.Frames
	return up || d.seekF.landed || d.seekF.moving
}

// videoSkippable is whether the video the player has is a DLNA video whose controller hears a next or
// a previous from the device (dlna.VideoSkippable): only then are they offered, never as buttons that
// do nothing.
func videoSkippable(st video.State) bool {
	return st.Origin == video.FromDLNA && st.Active() && dlna.Get().VideoSkippable(st.ID)
}

// videoSkip passes a back or a forward on the video page to the DLNA controller (dlna.Skip).
func videoSkip(t media.Transport) {
	if !dlna.Get().Skip(t) {
		slog.Info("screen: a skip on a video with nobody to hear it", "transport", t)
	}
}

// videoAsk is the question about a DLNA video, as drawn.
type videoAsk struct {
	id    uint64
	from  string
	title string
}

// videoStrip is the controls' shade: dark enough for white over any picture, light enough to see the
// picture through.
var videoStrip = color.RGBA{0x00, 0x00, 0x00, 0xb0}

// videoTrack is the bar to seek on where the video has not reached: white at a third, premultiplied as
// the canvas is, so the strip shows through it as the picture shows through the strip.
var videoTrack = color.RGBA{0x55, 0x55, 0x55, 0x55}

// videoTime is a place in a video as a player says it: 4:05, or 1:04:05 past an hour.
func videoTime(d time.Duration) string {
	sec := int(max(d, 0) / time.Second)
	if sec >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", sec/3600, sec/60%60, sec%60)
	}
	return fmt.Sprintf("%d:%02d", sec/60, sec%60)
}

// askSettle is how long a DLNA video's question has to be on the screen before a tap answers it: a
// finger already on its way to whatever was there must not answer a question that has just come up.
const askSettle = 500 * time.Millisecond

// askLatch is the DLNA video question the screen last drew, and when it first drew it. A tap answers
// that question and no other: a request that changed it since is not what the finger saw.
type askLatch struct {
	mu sync.Mutex
	id uint64
	at time.Time
}

// drawn is the frame having drawn question id (0: none).
func (l *askLatch) drawn(id uint64, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if id != l.id {
		l.id, l.at = id, now
	}
}

// answerable is the question a tap now answers: the one drawn, once it has been up askSettle.
func (l *askLatch) answerable(now time.Time) (uint64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.id, l.id != 0 && now.Sub(l.at) >= askSettle
}

// videoPainter is the frame loop's hold on a video's frames: the frame on the panel (kept until the
// next is up, so the controls can be put over it again), and whether the panel is dark, when the frames
// are taken as they fall due and not shown.
type videoPainter struct {
	last  *video.Frame
	dark  bool
	black []byte
}

// blackFrame is a frame of black for the panel, made once.
func (p *videoPainter) blackFrame(dev *screen.Device) []byte {
	w, h := dev.FrameSize()
	if len(p.black) != w*h*4 {
		p.black = make([]byte, w*h*4)
		for i := 3; i < len(p.black); i += 4 {
			p.black[i] = 0xff
		}
	}
	return p.black
}

// drop gives back the frame kept for the panel once the video page is gone.
func (p *videoPainter) drop() {
	if p.last != nil {
		video.Get().Done(p.last)
		p.last = nil
	}
}

// paintVideo shows the video's frames as they fall due, until wait has passed, something wakes the
// frame loop, or there is no picture any more. redraw is the controls having changed: the frame on
// the panel is put up again with the new ones over it.
func (p *videoPainter) paint(ctx context.Context, dev *screen.Device, poke <-chan struct{}, wait time.Duration, over image.Rectangle) {
	deadline := time.Now().Add(wait)
	redraw := true
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		fr, due, ok := video.Get().Next()
		if !ok {
			return
		}
		switch {
		case fr != nil && p.dark:
			video.Get().Done(fr)
			continue
		case fr != nil:
			if err := dev.PresentFrame(fr.Pix, over); err != nil {
				slog.Warn("presenting a video frame failed", "err", err)
			}
			video.Get().Done(p.last)
			p.last, redraw = fr, false
			continue
		case redraw && !p.dark:
			// The frame on the panel again, with the controls as they are now; black under them when
			// there is none to hand (the page has just been uncovered), never the page that was over it.
			pix := p.blackFrame(dev)
			if p.last != nil {
				pix = p.last.Pix
			}
			if err := dev.PresentFrame(pix, over); err != nil {
				slog.Warn("presenting a video frame failed", "err", err)
			}
			redraw = false
		}
		due = max(due, 2*time.Millisecond)
		if left := time.Until(deadline); left < due {
			if left <= 0 {
				return
			}
			due = left
		}
		timer.Reset(due)
		select {
		case <-ctx.Done():
			return
		case <-poke:
			return
		case <-timer.C:
		}
	}
}

// videoShot is a screenshot of the video page: the frame on the panel turned back to the canvas's
// way up, with the canvas (the controls) over it.
func (p *videoPainter) shot(dev *screen.Device, canvas *image.RGBA) *image.RGBA {
	img := image.NewRGBA(canvas.Rect)
	if fr := p.last; fr != nil && dev != nil {
		pw, ph := dev.FrameSize()
		bgra := dev.PixFmt() == "bgra"
		w, h := img.Rect.Dx(), img.Rect.Dy()
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				px, py := x, y
				if dev.Rotated() {
					px, py = pw-1-y, x
				}
				if px < 0 || py < 0 || px >= pw || py >= ph {
					continue
				}
				i := (py*pw + px) * 4
				o := img.PixOffset(x, y)
				p := fr.Pix[i : i+4 : i+4]
				if bgra {
					img.Pix[o], img.Pix[o+1], img.Pix[o+2] = p[2], p[1], p[0]
				} else {
					img.Pix[o], img.Pix[o+1], img.Pix[o+2] = p[0], p[1], p[2]
				}
				img.Pix[o+3] = 255
			}
		}
	}
	// The controls, as the panel blends them.
	src := canvas
	for i := 0; i+3 < len(img.Pix); i += 4 {
		a := uint32(src.Pix[i+3])
		if a == 0 {
			continue
		}
		for k := 0; k < 3; k++ {
			img.Pix[i+k] = uint8(uint32(src.Pix[i+k]) + uint32(img.Pix[i+k])*(255-a)/255)
		}
	}
	return img
}
