//go:build spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// slideshowWash is the theme's ground color, translucent, over a photo — the same technique
// render_slideshow.go's Show version uses, just this theme's color (colBackground) instead of
// walnut. Light, because readable.go darkens a bright photo further just where the words go.
const slideshowWash = 90

// slideshowBackground draws a Background-mode photo full-bleed, then the wash over it.
func (r *roundRenderer) slideshowBackground(img *image.RGBA) {
	r.washed.lay(r.dst, img, color.RGBA{colBackground.R, colBackground.G, colBackground.B, slideshowWash})
}

// artWeather is the rain, snow, storm or fog moving over the weather art, when it is the picture.
func (r *roundRenderer) artWeather(s roundScene) {
	if s.artFx != fxNone {
		r.sky(s.artFx, s.now, r.dst.Rect, image.Rect(side/4, side/8, side*3/4, side/2))
		r.artDrawn = true
	}
}

// slideshowScreensaverFace is Screensaver mode: the photo full-bleed, and — unless the overlay is
// off — the wash plus a clock, small or normal size. No weather, no timers, no status label: those
// belong to clockFace's ordinary idle face, not the photo-frame look.
func (r *roundRenderer) slideshowScreensaverFace(s roundScene) {
	defer r.photoCountdown(s)
	if s.slideshowOverlay == config.SlideshowOverlayOff {
		draw.Draw(r.dst, r.dst.Rect, s.slideshowScreensaver, s.slideshowScreensaver.Bounds().Min, draw.Src)
		r.artWeather(s)
		if len(s.slideshowCaptions) > 0 {
			// No wash with no clock, and none for the words alone: readable.go darkens just under them.
			r.readableOver(s.slideshowScreensaver, colBackground, 0, func() { r.photoCaption(s.slideshowCaptions, false) })
		}
		return
	}
	// Washed as the background is, and kept for the second when it is the weather art (washedArt).
	r.washed.lay(r.dst, s.slideshowScreensaver, color.RGBA{colBackground.R, colBackground.G, colBackground.B, slideshowWash})
	r.artWeather(s)
	// The clock wanders a few pixels against image retention, as the clock face does
	// (clock_drift_spot.go); the photos change under it, the clock would not.
	r.drifted(spotClockDrift(s.now, false), func() {
		r.readableOver(s.slideshowScreensaver, colBackground, slideshowWash, func() {
			r.screensaverClock(s)
			// The date and place on the side the clock is not (#573).
			r.photoCaption(s.slideshowCaptions, s.slideshowClockAt == config.SlideshowAtBottom)
		})
	})
}

// photoCountdown is how much of the screensaver photo's time has gone (#656): a thin ring round the
// very edge of the face, filling clockwise from the top until the next photo, over a fainter one the
// whole way round that says where it ends. Both are the words' color, see-through, as faint as a ring
// can be and still be seen: the photo shows through, and they dim with the screen as the clock does.
// Muted is drawn over it, wider (mutedRim).
func (r *roundRenderer) photoCountdown(s roundScene) {
	if !s.saverCountdown {
		return
	}
	in := float64(rimOut - countdownWidth)
	r.arc(in, rimOut, 0, 2*math.Pi, color.RGBA{colText.R, colText.G, colText.B, countdownTrack})
	if done := min(max(s.saverDone, 0), 1); done > 0 {
		r.arc(in, rimOut, 0, 2*math.Pi*done, color.RGBA{colText.R, colText.G, colText.B, countdownFill})
	}
}

// countdownWidth is how wide the countdown's ring is, in pixels; countdownTrack and countdownFill how
// opaque its two parts are, out of 255.
const (
	countdownWidth = 4
	countdownTrack = 40
	countdownFill  = 110
)

// photoCaption writes when and where the photo was taken, centered low on the face where the circle
// is still wide enough for it: the place, and the day under it. top has it high on the face instead,
// for a clock along the foot (#573), the place still over the day.
func (r *roundRenderer) photoCaption(caps []home.PhotoCaption, top bool) {
	if len(caps) == 0 {
		return
	}
	c := caps[0]
	if top {
		y := 100
		if c.Place != "" {
			r.centered(r.tiny, clip(r.tiny, r, c.Place, side*2/3-2*spotDriftAcross), y, colText)
			y += 28
		}
		if !c.When.IsZero() {
			r.centered(r.tiny, locale.PhotoDate(c.When, screenLang()), y, colText)
		}
		return
	}
	y := side - 60
	if !c.When.IsZero() {
		r.centered(r.tiny, locale.PhotoDate(c.When, screenLang()), y, colText)
		y -= 28
	}
	if c.Place != "" {
		// Clipped short of the circle by the clock's drift either side, which moves it too.
		r.centered(r.tiny, clip(r.tiny, r, c.Place, side*2/3-2*spotDriftAcross), y, colText)
	}
}

// screensaverClock is clockFace's time-and-date lines alone, without its timer or status label: a
// small time near the top, a medium one with the date under it (#502), or clockFace's own layout. The
// weather, when it is asked for, is the sky's icon and the temperature under them.
func (r *roundRenderer) screensaverClock(s roundScene) {
	now := s.now
	at := s.slideshowClockAt
	switch s.slideshowOverlay {
	case config.SlideshowOverlaySmall:
		y := 60 // the time's line: high as it was, across the middle, or low where the circle still holds it
		switch at {
		case config.SlideshowAtCenter:
			y = 250
		case config.SlideshowAtBottom:
			y = 392
		}
		r.centered(r.small, clockText(now), y, colText)
		if s.saverWeather {
			r.saverWeather(s.weather, y+34)
		}
	case config.SlideshowOverlayMedium:
		// High on the face, where the circle is wide enough for it, and clear of the photo's middle; or
		// the whole block moved down: to the middle, or to where the last line is still inside the circle.
		dy := 0
		switch at {
		case config.SlideshowAtCenter:
			dy = 120
			if s.saverWeather {
				dy = 104
			}
		case config.SlideshowAtBottom:
			dy = 230
			if s.saverWeather {
				dy = 212
			}
		}
		r.centered(r.mid, clockText(now), 126+dy, colText)
		r.centered(r.small, locale.LongDate(now, screenLang()), 162+dy, colDim)
		if s.saverWeather {
			r.saverWeather(s.weather, 198+dy)
		}
	default:
		// Mid-face as it was (so is the top, here), or the whole block 70 lower for the bottom.
		dy := 0
		if at == config.SlideshowAtBottom {
			dy = 70
		}
		r.timeLine(now, 240+dy)
		r.centered(r.small, locale.LongDate(now, screenLang()), 290+dy, colDim)
		if s.saverWeather {
			r.saverWeather(s.weather, 328+dy)
		}
	}
}

// saverWeather is the weather with the screensaver's clock: the sky's icon and the temperature,
// centered on the face with the words' baseline at baseline.
func (r *roundRenderer) saverWeather(w home.Weather, baseline int) {
	icon := w.Condition != "" && conditionWords(w.Condition) != ""
	if w.Temp == "" && !icon {
		return
	}
	const u, gap = 13.0, 10
	width := r.width(r.small, w.Temp)
	if icon {
		width += int(2 * u)
		if w.Temp != "" {
			width += gap
		}
	}
	left := center - width/2
	if icon {
		r.weatherIcon(w.Condition, float64(left)+u, float64(baseline)-8, u)
		r.over.note(image.Rect(left, baseline-8-int(u), left+int(2*u), baseline-8+int(u)))
		left += int(2*u) + gap
	}
	r.text(r.small, w.Temp, left, baseline, colText)
}
