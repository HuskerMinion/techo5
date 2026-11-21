//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// slideshowWash is the theme's ground color, translucent, over a photo — the same technique the
// now-playing screen uses for cover art (render_nowplaying.go's background): the photo stays
// recognizable. Light, because readable.go darkens a bright photo further just where the words go.
const slideshowWash = 90

// slideshowBackground draws a Background-mode photo full-bleed, then the wash over it.
func (r *renderer) slideshowBackground(img *image.RGBA) {
	r.washed.lay(r.dst, img, washOf(walnut, slideshowWash))
}

// artWeather is the rain, snow, storm or fog moving over the weather art, when it is the picture.
func (r *renderer) artWeather(s scene) {
	if s.artFx != fxNone {
		r.sky(s.artFx, s.now, r.dst.Rect, image.Rect(r.w/5, 0, r.w*4/5, r.h/2))
		r.artDrawn = true
	}
}

// slideshowScreensaverPage is Screensaver mode: the photo full-bleed, and — unless the overlay is
// off — the wash plus a clock, small (cornerClock) or normal size (the same layout bigClock uses,
// without its weather and timers, which belong to the ordinary idle page).
func (r *renderer) slideshowScreensaverPage(s scene) {
	defer r.photoCountdown(s)
	if s.slideshowOverlay == config.SlideshowOverlayOff {
		draw.Draw(r.dst, r.dst.Rect, s.slideshowScreensaver, s.slideshowScreensaver.Bounds().Min, draw.Src)
		r.artWeather(s)
		if len(s.slideshowCaptions) > 0 {
			// No wash with no clock, and none for the words alone: readable.go darkens just under them.
			r.readableOver(s.slideshowScreensaver, walnut, 0, func() { r.photoCaptions(s.slideshowCaptions, false) })
		}
		return
	}
	// Washed as the background is, and kept for the second when it is the weather art (washedArt).
	r.washed.lay(r.dst, s.slideshowScreensaver, washOf(walnut, slideshowWash))
	r.artWeather(s)
	r.readableOver(s.slideshowScreensaver, walnut, slideshowWash, func() {
		// The date and place on the side the clock is not: the foot unless the clock is there (#573).
		at := s.slideshowClockAt
		r.photoCaptions(s.slideshowCaptions, at == config.SlideshowAtBottom)
		switch s.slideshowOverlay {
		case config.SlideshowOverlaySmall:
			// The time's line: at the top as it was, across the middle, or along the foot.
			y := r.margin + r.s(26)
			switch at {
			case config.SlideshowAtCenter:
				y = r.h/2 + r.s(10)
			case config.SlideshowAtBottom:
				y = r.h - r.margin
			}
			r.cornerClockAt(s, y)
			if s.saverWeather {
				// On the corner clock's line, just before it.
				t := clockText(s.now)
				r.saverWeather(s.weather, r.w-r.margin-r.width(r.small, t)-r.s(24), y, 1)
			}
		case config.SlideshowOverlayMedium:
			// The compact clock the corner styles use, at the right: the time at the size of the
			// weather's reading, the date under it, readable across a room and off the middle of the photo.
			// Its block runs from the time's baseline to the date's, or the weather's under it.
			below := r.s(48) // the date's baseline under the time's
			if s.saverWeather {
				below = r.s(102) // and the weather's
			}
			base := r.margin + r.s(84)
			switch at {
			case config.SlideshowAtCenter:
				base = r.h/2 - below/2 + r.s(24)
			case config.SlideshowAtBottom:
				base = r.h - r.margin - below
			}
			d := r.timeAndDateAt(s.now, base, "", 1, 0, true, 0)
			if s.saverWeather {
				r.saverWeather(s.weather, r.w-r.margin, d.Max.Y+r.s(44), 1)
			}
		default:
			// Centered across; down the screen it was in the middle, and stays so unless it is asked for
			// the foot. The digits keep the size they have in the middle (roof).
			base, roof := r.h/2+r.s(60), 0
			if at == config.SlideshowAtBottom {
				below := r.s(70) // the date's baseline under the time's
				if s.saverWeather {
					below = r.s(130) // and the weather's
				}
				base = r.h - r.margin - below
				roof = base - (r.h/2 + r.s(60))
			}
			d := r.timeAndDateAt(s.now, base, "", 0, 0, false, roof)
			if s.saverWeather {
				r.saverWeather(s.weather, r.w/2, d.Max.Y+r.s(50), 0)
			}
		}
	})
}

// photoCountdown is how much of the screensaver photo's time has gone (#656): a thin line along the
// very foot of the screen, filling from the left until the next photo, over a fainter one the whole
// way across that says where it ends. Both are the words' color, see-through, as faint as a line can
// be and still be seen: the photo shows through, and they dim with the screen as the clock does. A
// faint dark edge along the top of both keeps them seen on a bright photo, where a light line alone
// was lost.
func (r *renderer) photoCountdown(s scene) {
	if !s.saverCountdown {
		return
	}
	top := r.h - max(r.s(countdownThick), 2)
	edge := top - max(r.s(countdownEdge), 1)
	draw.Draw(r.dst, image.Rect(0, edge, r.w, top), image.NewUniform(color.RGBA{A: countdownShade}), image.Point{}, draw.Over)
	draw.Draw(r.dst, image.Rect(0, top, r.w, r.h), image.NewUniform(washOf(cream, countdownTrack)), image.Point{}, draw.Over)
	if done := int(float64(r.w)*min(max(s.saverDone, 0), 1) + 0.5); done > 0 {
		draw.Draw(r.dst, image.Rect(0, top, done, r.h), image.NewUniform(washOf(cream, countdownFill)), image.Point{}, draw.Over)
	}
}

// countdownThick is how thick the countdown's line is, in the Show 5's pixels; countdownTrack and
// countdownFill how opaque its two parts are, out of 255; countdownEdge how far the dark edge under it
// reaches above it, and countdownShade how dark that is.
const (
	countdownThick = 3
	countdownEdge  = 1
	countdownShade = 70
	countdownTrack = 40
	countdownFill  = 110
)

// saverWeather is the weather with the screensaver's clock (#502): the sky's icon and the temperature,
// nothing more, the words' baseline at base. align lines it up on x: -1 starting there, 0 centered on
// it, 1 ending there.
func (r *renderer) saverWeather(w home.Weather, x, base, align int) {
	icon := w.Condition != "" && conditionWords(w.Condition) != ""
	if w.Temp == "" && !icon {
		return
	}
	mark, gap := r.s(weatherMark), r.s(12)
	width := r.width(r.small, w.Temp)
	if icon {
		width += mark
		if w.Temp != "" {
			width += gap
		}
	}
	left := x
	switch align {
	case 0:
		left = x - width/2
	case 1:
		left = x - width
	}
	if icon {
		mid := base - r.s(11)
		r.weatherIcon(w.Condition, left+mark/2, mid, mark)
		r.over.note(image.Rect(left, mid-mark/2, left+mark, mid+mark/2))
		left += mark + gap
	}
	r.text(r.small, w.Temp, left, base, cream)
}

// photoCaptions writes when and where each photo up was taken in its bottom left corner: the place,
// and the day under it. Each of a pair has its own, in its own half. top has them in the top left corner
// instead, for a clock along the foot (#573), the place still over the day.
func (r *renderer) photoCaptions(caps []home.PhotoCaption, top bool) {
	for _, c := range caps {
		x, w := r.margin, r.w-2*r.margin
		if c.Pair {
			w = r.w/2 - 2*r.margin
			if c.Right {
				x += r.w / 2
			}
		}
		if top {
			y := r.margin + r.s(26)
			if c.Place != "" {
				r.text(r.tiny, r.clipTo(r.tiny, c.Place, w), x, y, cream)
				y += r.s(30)
			}
			if !c.When.IsZero() {
				r.text(r.tiny, r.clipTo(r.tiny, locale.PhotoDate(c.When, screenLang()), w), x, y, cream)
			}
			continue
		}
		y := r.h - r.margin
		if !c.When.IsZero() {
			r.text(r.tiny, r.clipTo(r.tiny, locale.PhotoDate(c.When, screenLang()), w), x, y, cream)
			y -= r.s(30)
		}
		if c.Place != "" {
			r.text(r.tiny, r.clipTo(r.tiny, c.Place, w), x, y, cream)
		}
	}
}
