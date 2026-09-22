//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// slideshowWash is the theme's ground color, translucent, over a photo — the same technique the
// now-playing screen uses for cover art (render_nowplaying.go's background): the photo stays
// recognisable, and the clock and date drawn over it stay legible.
const slideshowWash = 130

// slideshowBackground draws a Background-mode photo full-bleed, then the wash over it.
func (r *renderer) slideshowBackground(img *image.RGBA) {
	draw.Draw(r.dst, r.dst.Rect, img, img.Bounds().Min, draw.Src)
	wash := color.RGBA{walnut.R, walnut.G, walnut.B, slideshowWash}
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(wash), image.Point{}, draw.Over)
}

// slideshowScreensaverPage is Screensaver mode: the photo full-bleed, and — unless the overlay is
// off — the wash plus a clock, small (cornerClock) or normal size (the same layout bigClock uses,
// without its weather and timers, which belong to the ordinary idle page).
func (r *renderer) slideshowScreensaverPage(s scene) {
	draw.Draw(r.dst, r.dst.Rect, s.slideshowScreensaver, s.slideshowScreensaver.Bounds().Min, draw.Src)
	if s.slideshowOverlay == config.SlideshowOverlayOff {
		return
	}
	wash := color.RGBA{walnut.R, walnut.G, walnut.B, slideshowWash}
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(wash), image.Point{}, draw.Over)
	if s.slideshowOverlay == config.SlideshowOverlaySmall {
		r.cornerClock(s)
		return
	}
	r.timeAndDate(s.now, r.h/2+60, "")
}
