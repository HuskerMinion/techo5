//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
)

// What the video page draws on the canvas (video.go): while frames come, only the controls, over a
// clear canvas that the panel blends over the picture; before the first frame, and when a video could
// not be played, a page of its own. In the Show 5's pixels.
const (
	videoStripH  = 132 // the controls' strip along the foot: the scrub bar, then the buttons
	videoButtonW = 84
	videoButtonH = 76
	videoGap     = 16
	videoIcon    = 44
	videoBarH    = 14
	videoBarTap  = 10 // how far round the bar a finger still counts as on it, short of the buttons
)

// videoButtons are the controls, left to right: play or pause and stop, then the captions when this
// device may say whether they are on. The volume is not among them: the Show has buttons for it, and a
// film is watched with the room's own level (a swipe over the bar still moves it, volume_swipe.go).
func (r *renderer) videoButtons() (play, stop, subs image.Rectangle) {
	w, h, gap := r.s(videoButtonW), r.s(videoButtonH), r.s(videoGap)
	y := r.h - r.s(videoStripH) + r.s(videoBarH) + r.s(30)
	x := r.margin
	play = image.Rect(x, y, x+w, y+h)
	stop = play.Add(image.Pt(w+gap, 0))
	subs = stop.Add(image.Pt(w+gap, 0))
	return play, stop, subs
}

// videoBar is the scrub bar: the whole title along the width of the screen, at the top of the strip.
func (r *renderer) videoBar() image.Rectangle {
	y := r.h - r.s(videoStripH) + r.s(16)
	return image.Rect(r.margin, y, r.w-r.margin, y+r.s(videoBarH))
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

// videoWords is the page before the picture comes -- a spinner turning under the title, because
// "loading" is the one thing the wait is for -- or why it could not be played.
func (r *renderer) videoWords(s scene) {
	v := s.video
	name := v.Title
	if name == "" {
		name = v.Host
	}
	if s.demo && name != "" {
		name = "A film"
	}
	if !v.Active() && v.Err != "" {
		head, sub := tr("ui_this_video_can_t_be_played"), v.Err
		if s.demo {
			sub = "Server returned 404 Not Found"
		}
		cy := r.h/2 - r.s(40)
		r.text(r.title, head, (r.w-r.width(r.title, head))/2, cy, cream)
		room := r.w - 2*r.margin
		lines := r.wrapLines(r.small, sub, room)
		if len(lines) > 3 {
			lines = append(lines[:2], r.fit(r.small, lines[2]+" "+lines[3], room))
		}
		for i, l := range lines {
			r.text(r.small, l, (r.w-r.width(r.small, l))/2, cy+r.s(56)+i*r.s(42), dim)
		}
		hint := tr("tap_to_go_back")
		r.text(r.tiny, hint, (r.w-r.width(r.tiny, hint))/2, r.h-r.s(40), dim)
		return
	}
	cy := r.h/2 - r.s(30)
	r.videoSpinner(s.now, cy-r.s(56))
	r.text(r.title, tr("loading_video"), (r.w-r.width(r.title, tr("loading_video")))/2, cy+r.s(24), cream)
	if name != "" {
		r.text(r.small, r.fit(r.small, name, r.w-2*r.margin),
			(r.w-r.width(r.small, r.fit(r.small, name, r.w-2*r.margin)))/2, cy+r.s(84), dim)
	}
}

// videoSpinner is the ring that says the film is on its way: twelve dots, brightest at the head of the
// turn, which the frame loop redraws a few times a second while there is no picture (videoNext).
func (r *renderer) videoSpinner(now time.Time, cy int) {
	const dots = 12
	cx := r.w / 2
	rad := r.s(34)
	d := r.s(10)
	phase := float64(now.UnixNano()%int64(time.Second)) / float64(time.Second)
	for i := 0; i < dots; i++ {
		a := 2 * math.Pi * (float64(i)/dots - phase)
		x := cx + int(float64(rad)*math.Cos(a)) - d/2
		y := cy + int(float64(rad)*math.Sin(a)) - d/2
		// The dot leading the turn is the brightest; a whole turn behind it is all but gone.
		alpha := uint8(40 + 215*float64(dots-1-i)/float64(dots-1))
		col := color.RGBA{cream.R, cream.G, cream.B, alpha}
		draw.Draw(r.dst, image.Rect(x, y, x+d, y+d), image.NewUniform(col), image.Point{}, draw.Over)
	}
}

// videoControls draws the strip of controls and remembers where they are, for a tap.
func (r *renderer) videoControls(s scene) image.Rectangle {
	strip := image.Rect(0, r.h-r.s(videoStripH), r.w, r.h)
	op := draw.Src // over a clear canvas: the panel blends it over the picture
	if !s.videoLive {
		op = draw.Over // over the page's own black, which the panel shows as it is
	}
	draw.Draw(r.dst, strip, image.NewUniform(videoStrip), image.Point{}, op)

	// The scrub bar: the title along it, the part played in the accent, and a knob at the place.
	bar := r.videoBar()
	draw.Draw(r.dst, bar, image.NewUniform(color.RGBA{0x50, 0x50, 0x50, 0xff}), image.Point{}, draw.Over)
	frac := 0.0
	if s.video.Dur > 0 {
		frac = min(max(float64(s.video.Pos)/float64(s.video.Dur), 0), 1)
	}
	played := bar
	played.Max.X = bar.Min.X + int(frac*float64(bar.Dx()))
	if played.Max.X > played.Min.X {
		draw.Draw(r.dst, played, image.NewUniform(amber), image.Point{}, draw.Over)
	}
	knob := r.s(24)
	kx := min(max(played.Max.X-knob/2, bar.Min.X), bar.Max.X-knob)
	draw.Draw(r.dst, image.Rect(kx, bar.Min.Y+r.s(2)-knob/2+r.s(5), kx+knob, bar.Min.Y+r.s(2)+knob/2+r.s(5)),
		image.NewUniform(cream), image.Point{}, draw.Over)

	play, stop, subs := r.videoButtons()
	rad := float64(r.s(18))
	mark := "pause"
	if s.video.Phase == video.Paused {
		mark = "play"
	}
	caption := "subtitles-outline"
	if s.videoSubs {
		caption = "subtitles"
	}
	zones := []image.Rectangle{play, stop}
	buttons := []string{mark, "stop"}
	if s.videoSubsCan {
		zones = append(zones, subs)
		buttons = append(buttons, caption)
	}
	for i, b := range zones {
		style := btnSecondary
		if b == play {
			style = btnPrimary
		}
		fg := r.buttonFace(b, rad, style)
		r.mdiIcon(buttons[i], b.Min.X+(b.Dx()-r.s(videoIcon))/2, b.Min.Y+(b.Dy()-r.s(videoIcon))/2, videoIcon, fg)
	}
	// The name and where it is, right of the buttons.
	left := zones[len(zones)-1].Max.X + r.s(28)
	name := s.video.Title
	if name == "" {
		name = s.video.Host
	}
	if s.demo && name != "" {
		name = "A film"
	}
	at := videoTime(s.video.Pos)
	if s.video.Dur > 0 {
		at += " / " + videoTime(s.video.Dur)
	}
	if s.video.Phase == video.Loading || !s.video.Frames {
		at = tr("ui_loading")
	}
	mid := strip.Min.Y + r.s(videoStripH) - r.s(videoButtonH)/2 - r.s(4)
	r.text(r.small, r.fit(r.small, name, r.w-r.margin-left), left, mid-r.s(4), cream)
	r.text(r.tiny, at, left, mid+r.s(32), dim)

	r.zmu.Lock()
	r.videoZones = zones
	r.zmu.Unlock()
	return strip
}

// videoTapped is which control a tap landed on, as last drawn. A finger is a little wider than a
// button's edge.
func (r *renderer) videoTapped(at image.Point) videoTap {
	r.zmu.Lock()
	defer r.zmu.Unlock()
	for i, z := range r.videoZones {
		if !at.In(z.Inset(-r.s(8))) {
			continue
		}
		switch i {
		case 0:
			return videoTapPlay
		case 1:
			return videoTapStop
		default:
			return videoTapSubtitles
		}
	}
	return videoTapNone
}

// videoScrubAt is a tap on the scrub bar: how far into the title it is, 0 to 1, and whether the tap was
// on the bar at all. The bar is a wide target: a finger at arm's length is not exact.
func (r *renderer) videoScrubAt(at image.Point) (float64, bool) {
	bar := r.videoBar()
	if !at.In(bar.Inset(-r.s(videoBarTap))) {
		return 0, false
	}
	return min(max(float64(at.X-bar.Min.X)/float64(max(bar.Dx(), 1)), 0), 1), true
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
