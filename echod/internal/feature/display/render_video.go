//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
)

// What the video page draws on the canvas (video.go): while frames come, only the controls, over a
// clear canvas that the panel blends over the picture; before the first frame, and when a video could
// not be played, a page of its own. In the Show 5's pixels.
const (
	videoStripH  = 112 // the controls' strip along the foot
	videoButtonW = 84
	videoButtonH = 76
	videoGap     = 16
	videoIcon    = 44
	// videoSeekH is the strip grown above the buttons for the bar to seek on, when the video has one.
	videoSeekH = 34
)

// videoButtons are the controls, left to right: play or pause and stop on the left, quieter and louder
// on the right.
func (r *renderer) videoButtons() (play, stop, quieter, louder image.Rectangle) {
	w, h, gap := r.s(videoButtonW), r.s(videoButtonH), r.s(videoGap)
	y := r.h - r.s(videoStripH) + (r.s(videoStripH)-h)/2
	x := r.margin
	play = image.Rect(x, y, x+w, y+h)
	stop = play.Add(image.Pt(w+gap, 0))
	louder = image.Rect(r.w-r.margin-w, y, r.w-r.margin, y+h)
	quieter = louder.Sub(image.Pt(w+gap, 0))
	return play, stop, quieter, louder
}

// videoSkipButtons are the back and forward a DLNA video has while its controller listens
// (videoSkip): after stop, in the left group.
func (r *renderer) videoSkipButtons() (back, next image.Rectangle) {
	_, stop, _, _ := r.videoButtons()
	step := image.Pt(r.s(videoButtonW)+r.s(videoGap), 0)
	back = stop.Add(step)
	return back, back.Add(step)
}

// videoFitButton is the button for the way the picture takes the screen (videoFit): before quieter, in
// the right group, so the left group keeps play and stop where a hand knows them, and back and forward
// after them.
func (r *renderer) videoFitButton() image.Rectangle {
	_, _, quieter, _ := r.videoButtons()
	return quieter.Sub(image.Pt(r.s(videoButtonW)+r.s(videoGap), 0))
}

// videoPage draws the video page and says what of it goes over the picture.
func (r *renderer) videoPage(s scene) (over image.Rectangle) {
	r.zmu.Lock()
	r.videoZones = nil
	r.zmu.Unlock()
	if s.videoLive {
		fillCanvas(r.dst, color.Transparent)
	} else {
		fillCanvas(r.dst, color.Black)
		r.videoWords(s)
	}
	if s.videoControls && s.video.Active() {
		over = r.videoControls(s)
	}
	if s.showVolume {
		r.volumeBar(s)
		over = over.Union(image.Rect(0, r.h-r.s(volumeBarH), r.w, r.h))
	}
	return over
}

// videoWords are the page before the picture comes: what is loading, or why it could not be played.
func (r *renderer) videoWords(s scene) {
	v := s.video
	name := v.Title
	if name == "" {
		name = v.Host
	}
	if s.demo && name != "" {
		name = "A film"
	}
	head, sub := tr("loading_video"), name
	if !v.Active() && v.Err != "" {
		head, sub = tr("ui_this_video_can_t_be_played"), v.Err
		if s.demo {
			sub = "Server returned 404 Not Found"
		}
	}
	cy := r.h/2 - r.s(40)
	if v.Active() || v.Err == "" {
		cy = r.h/2 - r.s(20)
	}
	r.text(r.title, head, (r.w-r.width(r.title, head))/2, cy, cream)
	room := r.w - 2*r.margin
	lines := r.wrapLines(r.small, sub, room)
	if len(lines) > 3 {
		lines = append(lines[:2], r.fit(r.small, lines[2]+" "+lines[3], room))
	}
	for i, l := range lines {
		r.text(r.small, l, (r.w-r.width(r.small, l))/2, cy+r.s(56)+i*r.s(42), dim)
	}
	if !v.Active() {
		hint := tr("tap_to_go_back")
		r.text(r.tiny, hint, (r.w-r.width(r.tiny, hint))/2, r.h-r.s(40), dim)
	}
}

// videoControls draws the strip of controls and remembers where they are, for a tap.
func (r *renderer) videoControls(s scene) image.Rectangle {
	base := r.h - r.s(videoStripH)
	strip := image.Rect(0, base, r.w, r.h)
	seek := s.videoSeek.tappable()
	if seek {
		strip.Min.Y -= r.s(videoSeekH)
	}
	op := draw.Src // over a clear canvas: the panel blends it over the picture
	if !s.videoLive {
		op = draw.Over // over the page's own black, which the panel shows as it is
	}
	draw.Draw(r.dst, strip, image.NewUniform(videoStrip), image.Point{}, op)
	play, stop, quieter, louder := r.videoButtons()
	rad := float64(r.s(18))
	mark := "pause"
	if s.video.Phase == video.Paused {
		mark = "play"
	}
	type button struct {
		rect image.Rectangle
		icon string
	}
	buttons := []button{{play, mark}, {stop, "stop"}, {quieter, "volume-minus"}, {louder, "volume-plus"}}
	zones := []videoZone{{play, videoTapPlay}, {stop, videoTapStop}, {quieter, videoTapQuieter}, {louder, videoTapLouder}}
	lastLeft, firstRight := stop, quieter
	if s.videoSkip {
		back, next := r.videoSkipButtons()
		buttons = append(buttons, button{back, "skip-previous"}, button{next, "skip-next"})
		zones = append(zones, videoZone{back, videoTapBack}, videoZone{next, videoTapNext})
		lastLeft = next
	}
	if s.videoFit {
		fit := r.videoFitButton()
		buttons = append(buttons, button{fit, videoFitIcon(s.video.Aspect)})
		zones = append(zones, videoZone{fit, videoTapFit})
		firstRight = fit
	}
	for _, b := range buttons {
		style := btnSecondary
		if b.rect == play {
			style = btnPrimary
		}
		fg := r.buttonFace(b.rect, rad, style)
		r.mdiIcon(b.icon, b.rect.Min.X+(b.rect.Dx()-r.s(videoIcon))/2, b.rect.Min.Y+(b.rect.Dy()-r.s(videoIcon))/2, videoIcon, fg)
	}
	// The name and where it is, between the two groups.
	left, right := lastLeft.Max.X+r.s(28), firstRight.Min.X-r.s(28)
	name := s.video.Title
	if name == "" {
		name = s.video.Host
	}
	if s.demo && name != "" {
		name = "A film"
	}
	at := videoTime(s.videoSeek.at(s.video.Pos))
	if dur := videoLength(s); dur > 0 {
		at += " / " + videoTime(dur)
	}
	if (s.video.Phase == video.Loading || !s.video.Frames) && !s.videoSeek.shown {
		at = tr("ui_loading")
	}
	mid := base + r.s(videoStripH)/2
	if seek {
		// Along the top of the strip, over the buttons and the width of the page: the whole of the
		// video from one margin to the other, for a finger to place a tap in.
		r.seekBar(s.videoSeek, r.margin, r.w-r.margin, strip.Min.Y+r.s(videoSeekH)/2+r.s(4), s.video.Pos, s.videoSeek.dur,
			videoTrack, amber)
	}
	r.text(r.small, r.fit(r.small, name, right-left), left, mid-r.s(4), cream)
	r.text(r.tiny, at, left, mid+r.s(32), dim)

	r.zmu.Lock()
	r.videoZones = zones
	r.zmu.Unlock()
	return strip
}

// videoLength is how long the video on the page runs: the player's say, or the DLNA renderer's when the
// player has none (a continuous transcode, whose controller can still be asked to move through it).
func videoLength(s scene) time.Duration {
	if s.videoSeek.start > 0 {
		return s.videoSeek.dur // the whole film's: the stream playing is only the rest of it
	}
	if s.video.Dur > 0 {
		return s.video.Dur
	}
	return s.videoSeek.dur
}

// videoTapped is which control a tap landed on, as last drawn. A finger is a little wider than a
// button's edge.
func (r *renderer) videoTapped(at image.Point) videoTap {
	r.zmu.Lock()
	defer r.zmu.Unlock()
	return videoZoneAt(r.videoZones, at, r.s(8))
}

// videoAskPage is the question about a DLNA video from an address that has not been allowed: who is
// asking and what it is, Not now on the left, Allow on the right, as the setup page's question is.
func (r *renderer) videoAskPage(s scene) {
	from, title := s.videoAsk.from, s.videoAsk.title
	if s.demo {
		from, title = "192.168.1.30", "A film"
	}
	r.text(r.title, tr("ui_video"), r.margin, r.s(96), amber)
	r.text(r.body, r.fit(r.body, fmt.Sprintf(tr("ui_video_from_s"), from), r.w-2*r.margin), r.margin, r.s(176), cream)
	if title != "" {
		r.text(r.small, r.fit(r.small, title, r.w-2*r.margin), r.margin, r.s(224), dim)
	}
	r.text(r.tiny, fmt.Sprintf(tr("ui_video_hint"), tr("ui_allow"), tr("not_now")), r.margin, r.s(268), dim)

	no, yes := r.actionHalves()
	rad := float64(r.s(actionRadius))
	mid := (no.Min.Y + no.Max.Y) / 2
	fg := r.buttonFace(no, rad, btnSecondary)
	r.text(r.body, tr("not_now"), no.Min.X+(no.Dx()-r.width(r.body, tr("not_now")))/2, mid+r.s(14), fg)
	fg = r.buttonFace(yes, rad, btnPrimary)
	r.text(r.title, tr("ui_allow"), yes.Min.X+(yes.Dx()-r.width(r.title, tr("ui_allow")))/2, mid+r.s(16), fg)
}
