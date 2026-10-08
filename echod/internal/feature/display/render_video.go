//go:build !dot && !spot

package display

import (
	"image"
	"image/draw"

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

// videoPage draws the video page and says what of it goes over the picture.
func (r *renderer) videoPage(s scene) (over image.Rectangle) {
	r.zmu.Lock()
	r.videoZones = nil
	r.zmu.Unlock()
	if s.videoLive {
		draw.Draw(r.dst, r.dst.Rect, image.Transparent, image.Point{}, draw.Src)
	} else {
		draw.Draw(r.dst, r.dst.Rect, image.Black, image.Point{}, draw.Src)
		r.videoWords(s)
	}
	if s.videoControls && s.video.Active() {
		over = r.videoControls(s)
	}
	if s.showVolume {
		r.volumeBar(s)
		over = over.Union(image.Rect(0, r.h-110, r.w, r.h))
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
	head, sub := "Loading video…", name
	if !v.Active() && v.Err != "" {
		head, sub = "This video can't be played", v.Err
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
		hint := "Tap to go back"
		r.text(r.tiny, hint, (r.w-r.width(r.tiny, hint))/2, r.h-r.s(40), dim)
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
	play, stop, quieter, louder := r.videoButtons()
	rad := float64(r.s(18))
	mark := "pause"
	if s.video.Phase == video.Paused {
		mark = "play"
	}
	for _, b := range []struct {
		rect image.Rectangle
		icon string
	}{{play, mark}, {stop, "stop"}, {quieter, "volume-minus"}, {louder, "volume-plus"}} {
		style := btnSecondary
		if b.rect == play {
			style = btnPrimary
		}
		fg := r.buttonFace(b.rect, rad, style)
		r.mdiIcon(b.icon, b.rect.Min.X+(b.rect.Dx()-r.s(videoIcon))/2, b.rect.Min.Y+(b.rect.Dy()-r.s(videoIcon))/2, videoIcon, fg)
	}
	// The name and where it is, between the two groups.
	left, right := stop.Max.X+r.s(28), quieter.Min.X-r.s(28)
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
		at = "Loading…"
	}
	mid := strip.Min.Y + strip.Dy()/2
	r.text(r.small, r.fit(r.small, name, right-left), left, mid-r.s(4), cream)
	r.text(r.tiny, at, left, mid+r.s(32), dim)

	r.zmu.Lock()
	r.videoZones = []image.Rectangle{play, stop, quieter, louder}
	r.zmu.Unlock()
	return strip
}

// videoTapped is which control a tap landed on, as last drawn. A finger is a little wider than a
// button's edge.
func (r *renderer) videoTapped(at image.Point) videoTap {
	r.zmu.Lock()
	defer r.zmu.Unlock()
	for i, z := range r.videoZones {
		if at.In(z.Inset(-r.s(8))) {
			return videoTap(i + 1)
		}
	}
	return videoTapNone
}

// videoAskPage is the question about a DLNA video from an address that has not been allowed: who is
// asking and what it is, Not now on the left, Allow on the right, as the setup page's question is.
func (r *renderer) videoAskPage(s scene) {
	from, title := s.videoAsk.from, s.videoAsk.title
	if s.demo {
		from, title = "192.168.1.30", "A film"
	}
	r.text(r.title, "Video", r.margin, r.s(96), amber)
	r.text(r.body, r.fit(r.body, "Show a video from "+from+"?", r.w-2*r.margin), r.margin, r.s(176), cream)
	if title != "" {
		r.text(r.small, r.fit(r.small, title, r.w-2*r.margin), r.margin, r.s(224), dim)
	}
	r.text(r.tiny, "Allow remembers this address. Not now asks again later.", r.margin, r.s(268), dim)

	no, yes := r.actionHalves()
	rad := float64(r.s(actionRadius))
	mid := (no.Min.Y + no.Max.Y) / 2
	fg := r.buttonFace(no, rad, btnSecondary)
	r.text(r.body, "Not now", no.Min.X+(no.Dx()-r.width(r.body, "Not now"))/2, mid+r.s(14), fg)
	fg = r.buttonFace(yes, rad, btnPrimary)
	r.text(r.title, "Allow", yes.Min.X+(yes.Dx()-r.width(r.title, "Allow"))/2, mid+r.s(16), fg)
}
