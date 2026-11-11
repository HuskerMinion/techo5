//go:build spot

package display

import (
	"image"
	"image/color"
	"image/draw"

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
	if s.slideshowOverlay == config.SlideshowOverlayOff {
		draw.Draw(r.dst, r.dst.Rect, s.slideshowScreensaver, s.slideshowScreensaver.Bounds().Min, draw.Src)
		r.artWeather(s)
		if len(s.slideshowCaptions) > 0 {
			// No wash with no clock, and none for the words alone: readable.go darkens just under them.
			r.readableOver(s.slideshowScreensaver, colBackground, 0, func() { r.photoCaption(s.slideshowCaptions) })
		}
		return
	}
	// Washed as the background is, and kept for the second when it is the weather art (washedArt).
	r.washed.lay(r.dst, s.slideshowScreensaver, color.RGBA{colBackground.R, colBackground.G, colBackground.B, slideshowWash})
	r.artWeather(s)
	r.readableOver(s.slideshowScreensaver, colBackground, slideshowWash, func() {
		r.screensaverClock(s)
		r.photoCaption(s.slideshowCaptions)
	})
}

// photoCaption writes when and where the photo was taken, centered low on the face where the circle
// is still wide enough for it: the place, and the day under it.
func (r *roundRenderer) photoCaption(caps []home.PhotoCaption) {
	if len(caps) == 0 {
		return
	}
	c := caps[0]
	y := side - 60
	if !c.When.IsZero() {
		r.centered(r.tiny, locale.PhotoDate(c.When, screenLang()), y, colText)
		y -= 28
	}
	if c.Place != "" {
		r.centered(r.tiny, clip(r.tiny, r, c.Place, side*2/3), y, colText)
	}
}

// screensaverClock is clockFace's time-and-date lines alone, without its timer or status label: a
// small time near the top, a medium one with the date under it (#502), or clockFace's own layout. The
// weather, when it is asked for, is the sky's icon and the temperature under them.
func (r *roundRenderer) screensaverClock(s roundScene) {
	now := s.now
	switch s.slideshowOverlay {
	case config.SlideshowOverlaySmall:
		r.centered(r.small, clockText(now), 60, colText)
		if s.saverWeather {
			r.saverWeather(s.weather, 94)
		}
	case config.SlideshowOverlayMedium:
		// High on the face, where the circle is wide enough for it, and clear of the photo's middle.
		r.centered(r.mid, clockText(now), 126, colText)
		r.centered(r.small, locale.LongDate(now, screenLang()), 162, colDim)
		if s.saverWeather {
			r.saverWeather(s.weather, 198)
		}
	default:
		r.timeLine(now, 240)
		r.centered(r.small, locale.LongDate(now, screenLang()), 290, colDim)
		if s.saverWeather {
			r.saverWeather(s.weather, 328)
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
