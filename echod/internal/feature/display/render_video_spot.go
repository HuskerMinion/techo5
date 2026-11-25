//go:build spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
)

// The Spot's video face (video_spot.go). The decoder fills the square with the picture and the bezel
// takes its corners (video.FitIn). The controls lie across the foot of the circle, where its width is:
// the title, four buttons on a chord the circle is wide enough for (five, a little narrower, with the
// one for the way the picture takes the screen), the time under them.
const (
	videoStripTopSpot = 300 // the controls' shade, down to the foot
	videoTitleYSpot   = 334 // the title's baseline
	videoButtonsYSpot = 346 // the buttons' top
	videoButtonWSpot  = 64
	videoButtonHSpot  = 58
	videoGapSpot      = 10
	videoTimeYSpot    = 436 // the time's baseline
	videoIconSpot     = 36
	// The bar to seek on, when the video has one: the shade starts higher, and the bar runs across the
	// circle above the title, where it is nearly at its widest.
	videoSeekTopSpot  = 270
	videoSeekYSpot    = 292 // the bar's middle
	videoSeekHalfSpot = 170 // half its length
	videoSeekThick    = 6
	videoSeekReach    = 16 // how far above and below it a finger still lands on it
)

// The five buttons with the one for the way the picture takes the screen (videoFit): narrower, so the
// row stays on the chord the circle is wide enough for at the buttons' foot (about 328 inside the
// bezel's margin there), 320 across.
const (
	videoButtonWFitSpot = 56
	videoGapFitSpot     = 10
)

// videoButtonsSpot are the controls, left to right: play or pause, stop, then with fit the button for
// the way the picture takes the screen, between them and the volume as the Show has it beside the
// volume, then quieter and louder.
func (r *roundRenderer) videoButtonsSpot(fit bool) []videoZone {
	taps := []videoTap{videoTapPlay, videoTapStop, videoTapQuieter, videoTapLouder}
	w, gap := videoButtonWSpot, videoGapSpot
	if fit {
		taps = []videoTap{videoTapPlay, videoTapStop, videoTapFit, videoTapQuieter, videoTapLouder}
		w, gap = videoButtonWFitSpot, videoGapFitSpot
	}
	total := len(taps)*w + (len(taps)-1)*gap
	x := center - total/2
	b := make([]videoZone, len(taps))
	for i, t := range taps {
		b[i] = videoZone{image.Rect(x, videoButtonsYSpot, x+w, videoButtonsYSpot+videoButtonHSpot), t}
		x += w + gap
	}
	return b
}

// videoFace draws the video face and says what of it goes over the picture.
func (r *roundRenderer) videoFace(s roundScene) (over image.Rectangle) {
	r.zmu.Lock()
	r.videoZones = nil
	r.zmu.Unlock()
	if s.videoLive {
		fillCanvas(r.dst, color.Transparent)
	} else {
		fillCanvas(r.dst, color.Black)
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
	head, sub := tr("loading_video"), name
	if !v.Active() && v.Err != "" {
		head, sub = tr("ui_can_t_play_it"), v.Err
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
		r.centered(r.tiny, tr("tap_to_go_back"), 400, colDim)
	}
}

// videoControlsSpot draws the controls across the foot of the circle and remembers where they are.
func (r *roundRenderer) videoControlsSpot(s roundScene) image.Rectangle {
	strip := image.Rect(0, videoStripTopSpot, side, side)
	seek := s.videoSeek.tappable()
	if seek {
		strip.Min.Y = videoSeekTopSpot
	}
	op := draw.Src
	if !s.videoLive {
		op = draw.Over
	}
	draw.Draw(r.dst, strip, image.NewUniform(videoStrip), image.Point{}, op)
	if seek {
		r.videoSeekBarSpot(s)
	}
	name := s.video.Title
	if name == "" {
		name = s.video.Host
	}
	r.centered(r.small, r.paint.fit(r.small, name, 300), videoTitleYSpot, colText)
	mark := "pause"
	if s.video.Phase == video.Paused {
		mark = "play"
	}
	b := r.videoButtonsSpot(s.videoFit)
	icons := map[videoTap]string{videoTapPlay: mark, videoTapStop: "stop", videoTapFit: videoFitIcon(s.video.Aspect),
		videoTapQuieter: "volume-minus", videoTapLouder: "volume-plus"}
	for _, z := range b {
		style := btnSecondary
		if z.tap == videoTapPlay {
			style = btnPrimary
		}
		fg := r.buttonFace(z.r, 16, style)
		r.mdiIcon(icons[z.tap], z.r.Min.X+(z.r.Dx()-videoIconSpot)/2, z.r.Min.Y+(z.r.Dy()-videoIconSpot)/2, videoIconSpot, fg)
	}
	at := videoTime(s.videoSeek.at(s.video.Pos))
	dur := s.video.Dur
	if dur <= 0 || s.videoSeek.start > 0 {
		// The DLNA renderer's say, for a stream the player has no length for, or one that is only
		// the rest of the film.
		dur = s.videoSeek.dur
	}
	if dur > 0 {
		at += " / " + videoTime(dur)
	}
	switch {
	case s.videoSeek.shown:
		// The place a finger is choosing on the bar, whatever else the line would say.
	case s.showVolume:
		at = fmt.Sprintf("%s %d%%", tr("volume"), s.volume*100/media.VolumeSteps)
	case s.video.Phase == video.Loading || !s.video.Frames:
		at = tr("ui_loading")
	}
	r.centered(r.small, at, videoTimeYSpot, colDim)
	r.zmu.Lock()
	r.videoZones = b
	r.zmu.Unlock()
	return strip
}

// videoSeekBarSpot draws the bar to seek on across the circle and says where it is, for a finger: the
// track, filled in the accent as far as the video has reached, and a knob where playback is or a finger is choosing.
func (r *roundRenderer) videoSeekBarSpot(s roundScene) {
	v := s.videoSeek
	top := videoSeekYSpot - videoSeekThick/2
	bar := image.Rect(center-videoSeekHalfSpot, top, center+videoSeekHalfSpot, top+videoSeekThick)
	draw.Draw(r.dst, bar, image.NewUniform(videoTrack), image.Point{}, draw.Over)
	done := bar
	done.Max.X = bar.Min.X + seekFill(bar.Dx(), v.at(s.video.Pos), v.dur)
	draw.Draw(r.dst, done, image.NewUniform(colListening), image.Point{}, draw.Over)
	// A knob where playback is (a fill alone can be too short to see), larger where a finger is
	// choosing.
	knob := 7.0
	if v.shown {
		knob = 10
	}
	r.discAt(float64(done.Max.X), videoSeekYSpot, knob, colListening)
	reach := image.Rect(bar.Min.X-8, videoSeekYSpot-videoSeekReach, bar.Max.X+8, videoSeekYSpot+videoSeekReach)
	r.seekBars.drew(seekZone{view: v, bar: bar, reach: reach})
}

// videoTappedSpot is which control a tap landed on, as last drawn.
func (r *roundRenderer) videoTappedSpot(at image.Point) videoTap {
	r.zmu.Lock()
	defer r.zmu.Unlock()
	return videoZoneAt(r.videoZones, at, 6)
}

// videoAskFace is the question about a DLNA video, the setup page's question's way: Not now above,
// Allow below, where the circle is widest.
func (r *roundRenderer) videoAskFace(s roundScene) {
	r.centered(r.title, tr("ui_video"), 120, colText)
	r.centered(r.small, tr("ui_video_request_prompt"), 166, colDim)
	r.centered(r.small, s.videoAsk.from+"?", 196, colText)
	if s.videoAsk.title != "" {
		r.centered(r.tiny, r.paint.fit(r.tiny, s.videoAsk.title, 320), 226, colDim)
	}
	fc := r.faces()
	right := func(label string) int { return center + (r.paint.width(fc.button, label)+44)/2 }
	r.pillButton(right(tr("not_now")), askNoY, tr("not_now"), btnSecondary)
	r.pillButton(right(tr("ui_allow")), askYesY, tr("ui_allow"), btnPrimary)
}
