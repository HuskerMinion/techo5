//go:build spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// slideshowWash is the theme's ground colour, translucent, over a photo — the same technique
// render_slideshow.go's Show version uses, just this theme's colour (colBackground) instead of
// walnut: the photo stays recognisable, and the clock drawn over it stays legible.
const slideshowWash = 130

// slideshowBackground draws a Background-mode photo full-bleed, then the wash over it.
func (r *roundRenderer) slideshowBackground(img *image.RGBA) {
	draw.Draw(r.dst, r.dst.Rect, img, img.Bounds().Min, draw.Src)
	wash := color.RGBA{colBackground.R, colBackground.G, colBackground.B, slideshowWash}
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(wash), image.Point{}, draw.Over)
}

// slideshowScreensaverFace is Screensaver mode: the photo full-bleed, and — unless the overlay is
// off — the wash plus a clock, small or normal size. No weather, no timers, no status label: those
// belong to clockFace's ordinary idle face, not the photo-frame look.
func (r *roundRenderer) slideshowScreensaverFace(s roundScene) {
	draw.Draw(r.dst, r.dst.Rect, s.slideshowScreensaver, s.slideshowScreensaver.Bounds().Min, draw.Src)
	if s.slideshowOverlay == config.SlideshowOverlayOff {
		return
	}
	wash := color.RGBA{colBackground.R, colBackground.G, colBackground.B, slideshowWash}
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(wash), image.Point{}, draw.Over)
	r.screensaverClock(s, s.slideshowOverlay != config.SlideshowOverlaySmall)
}

// screensaverClock is clockFace's time-and-date lines alone, without its weather/timer/status
// label. big is the normal size (clockFace's own layout); otherwise a small one near the top.
func (r *roundRenderer) screensaverClock(s roundScene, big bool) {
	if !big {
		timeFmt := "3:04 PM"
		if s.time24h {
			timeFmt = "15:04"
		}
		r.centred(r.small, s.now.Format(timeFmt), 60, colText)
		return
	}
	now := s.now
	if s.time24h {
		r.centred(r.clock, now.Format("15:04"), 240, colText)
		r.centred(r.small, now.Format("Monday, January 2"), 300, colDim)
	} else {
		r.centred(r.clock, now.Format("3:04"), 240, colText)
		r.centred(r.small, strings.ToUpper(now.Format("PM")), 290, colDim)
		r.centred(r.small, now.Format("Monday, January 2"), 330, colDim)
	}
}
