//go:build spot

package display

import (
	"image"
	"image/color"
	"image/draw"

	"github.com/HuskerMinion/techo5/echod/internal/config"
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
		return
	}
	// Washed as the background is, and kept for the second when it is the weather art (washedArt).
	r.washed.lay(r.dst, s.slideshowScreensaver, color.RGBA{colBackground.R, colBackground.G, colBackground.B, slideshowWash})
	r.artWeather(s)
	r.readableOver(s.slideshowScreensaver, colBackground, slideshowWash, func() {
		r.screensaverClock(s, s.slideshowOverlay != config.SlideshowOverlaySmall)
	})
}

// screensaverClock is clockFace's time-and-date lines alone, without its weather/timer/status
// label. big is the normal size (clockFace's own layout); otherwise a small one near the top.
func (r *roundRenderer) screensaverClock(s roundScene, big bool) {
	if !big {
		r.centered(r.small, clockText(s.now), 60, colText)
		return
	}
	now := s.now
	r.timeLine(now, 240)
	r.centered(r.small, locale.LongDate(now, screenLang()), 290, colDim)
}
