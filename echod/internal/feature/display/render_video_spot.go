//go:build spot

package display

import (
	"fmt"
	"image"
	"image/draw"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
)

// The Spot's video face (video_spot.go). The decoder fills the square with the picture and the bezel
// takes its corners (video.FitIn). The controls lie across the foot of the circle, where its width is:
// the title, four buttons on a chord the circle is wide enough for, the time under them.
const (
	videoStripTopSpot = 300 // the controls' shade, down to the foot
	videoTitleYSpot   = 334 // the title's baseline
	videoButtonsYSpot = 346 // the buttons' top
	videoButtonWSpot  = 64
	videoButtonHSpot  = 58
	videoGapSpot      = 10
	videoTimeYSpot    = 436 // the time's baseline
	videoIconSpot     = 36
)

// videoButtonsSpot are the controls, left to right: play or pause, stop, quieter, louder.
func (r *roundRenderer) videoButtonsSpot() [4]image.Rectangle {
	total := 4*videoButtonWSpot + 3*videoGapSpot
	x := center - total/2
	var b [4]image.Rectangle
	for i := range b {
		b[i] = image.Rect(x, videoButtonsYSpot, x+videoButtonWSpot, videoButtonsYSpot+videoButtonHSpot)
		x += videoButtonWSpot + videoGapSpot
	}
	return b
}

// videoFace draws the video face and says what of it goes over the picture.
func (r *roundRenderer) videoFace(s roundScene) (over image.Rectangle) {
	r.zmu.Lock()
	r.videoZones = nil
	r.zmu.Unlock()
	if s.videoLive {
		draw.Draw(r.dst, r.dst.Rect, image.Transparent, image.Point{}, draw.Src)
	} else {
		draw.Draw(r.dst, r.dst.Rect, image.Black, image.Point{}, draw.Src)
		r.videoWordsSpot(s)
	}
	if s.videoControls && s.video.Active() {
		over = r.videoControlsSpot(s)
	}
	return over
}

// videoWordsSpot is the face before the picture comes, or saying why it could not be played.
func (r *roundRenderer) videoWordsSpot(s roundScene) {
	v := s.video
	name := v.Title
	if name == "" {
		name = v.Host
	}
	head, sub := "Loading video…", name
	if !v.Active() && v.Err != "" {
		head, sub = "Can't play it", v.Err
	}
	r.centered(r.title, head, 180, colText)
	lines := r.paint.wrapLines(r.small, sub, 360)
	if len(lines) > 3 {
		lines = append(lines[:2], r.paint.fit(r.small, lines[2]+" "+lines[3], 360))
	}
	for i, l := range lines {
		r.centered(r.small, l, 222+i*30, colDim)
	}
	if !v.Active() {
		r.centered(r.tiny, "Tap to go back", 400, colDim)
	}
}

// videoControlsSpot draws the controls across the foot of the circle and remembers where they are.
func (r *roundRenderer) videoControlsSpot(s roundScene) image.Rectangle {
	strip := image.Rect(0, videoStripTopSpot, side, side)
	op := draw.Src
	if !s.videoLive {
		op = draw.Over
	}
	draw.Draw(r.dst, strip, image.NewUniform(videoStrip), image.Point{}, op)
	name := s.video.Title
	if name == "" {
		name = s.video.Host
	}
	r.centered(r.small, r.paint.fit(r.small, name, 300), videoTitleYSpot, colText)
	mark := "pause"
	if s.video.Phase == video.Paused {
		mark = "play"
	}
	b := r.videoButtonsSpot()
	for i, icon := range []string{mark, "stop", "volume-minus", "volume-plus"} {
		style := btnSecondary
		if i == 0 {
			style = btnPrimary
		}
		fg := r.buttonFace(b[i], 16, style)
		r.mdiIcon(icon, b[i].Min.X+(b[i].Dx()-videoIconSpot)/2, b[i].Min.Y+(b[i].Dy()-videoIconSpot)/2, videoIconSpot, fg)
	}
	at := videoTime(s.video.Pos)
	if s.video.Dur > 0 {
		at += " / " + videoTime(s.video.Dur)
	}
	switch {
	case s.showVolume:
		at = fmt.Sprintf("Volume %d%%", s.volume*100/media.VolumeSteps)
	case s.video.Phase == video.Loading || !s.video.Frames:
		at = "Loading…"
	}
	r.centered(r.small, at, videoTimeYSpot, colDim)
	r.zmu.Lock()
	r.videoZones = b[:]
	r.zmu.Unlock()
	return strip
}

// videoTappedSpot is which control a tap landed on, as last drawn.
func (r *roundRenderer) videoTappedSpot(at image.Point) videoTap {
	r.zmu.Lock()
	defer r.zmu.Unlock()
	for i, z := range r.videoZones {
		if at.In(z.Inset(-6)) {
			return videoTap(i + 1)
		}
	}
	return videoTapNone
}

// videoAskFace is the question about a DLNA video, the setup page's question's way: Not now above,
// Allow below, where the circle is widest.
func (r *roundRenderer) videoAskFace(s roundScene) {
	r.centered(r.title, "Video", 120, colText)
	r.centered(r.small, "Show a video from", 166, colDim)
	r.centered(r.small, s.videoAsk.from+"?", 196, colText)
	if s.videoAsk.title != "" {
		r.centered(r.tiny, r.paint.fit(r.tiny, s.videoAsk.title, 320), 226, colDim)
	}
	fc := r.faces()
	right := func(label string) int { return center + (r.paint.width(fc.button, label)+44)/2 }
	r.pillButton(right("Not now"), askNoY, "Not now", btnSecondary)
	r.pillButton(right("Allow"), askYesY, "Allow", btnPrimary)
}
