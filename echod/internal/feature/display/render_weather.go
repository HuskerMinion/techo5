//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"time"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// weatherShow is how long the weather page stays after the question that brought it up.
const weatherShow = 30 * time.Second

// weatherRange is today's high and low, in the screen's language. The Spot has drawn these words
// from the catalog for a while; the Show had them hardcoded, which left them English everywhere.
func weatherRange(high, low float64) string {
	return fmt.Sprintf("%s %.0f°   %s %.0f°", tr("weather_high"), high, tr("weather_low"), low)
}

// weatherRain is today's chance of rain, in the screen's language.
func weatherRain(rain int) string {
	return fmt.Sprintf("%s %d%%", tr("weather_rain_probability"), rain)
}

// weatherPage is today large on the left and the next days as columns on the right.
// weatherPageKept is weatherPage, drawn only when what it shows has changed and copied otherwise: the
// page is most of a frame's cost (its faded icon is a whole panel blended), and a moving sky asks for a
// frame every 83 ms. What it shows is the reading, the forecast, the corner clock and the colors.
func (r *renderer) weatherPageKept(s scene) {
	key := fmt.Sprint(clockText(s.now), s.weather, s.forecast, r.w, r.h, walnut, cream, amber, dim, ember, screenLang())
	if key == r.weatherKey && len(r.weatherKept) == len(r.dst.Pix) {
		copy(r.dst.Pix, r.weatherKept)
		return
	}
	// Kept with no finger on it, or a press on its button would stay in the copy: the page shows none.
	press := r.press
	r.press.on = false
	r.weatherPage(s)
	r.press = press
	r.weatherKept = append(r.weatherKept[:0], r.dst.Pix...)
	r.weatherKey = key
}

// weatherSpots is where the weather page puts today: the icon's middle and size, the reading's
// baseline, and the baselines of the condition, the day's range and its rain. The page is composed
// for the Show 5's shape; a panel taller for its width (the Show 8, the Skylight) gives a quarter of
// what it has over to the room above and half to the room inside the page, so its lower half is not
// left empty under a page drawn at the top. Today's lines keep together, in the middle of the boxes'
// height; the boxes stretch, their figures moving down to the foot.
type weatherSpots struct {
	icon            image.Point
	iconSize        int
	reading         image.Point
	cond, rng, rain int
	boxTop, boxFoot int
	name, dayIcon   int
	dayIconSize     int
	high, low, perc int
}

// weatherLayout is the page's spots on this panel; colW is a forecast column's width, which bounds
// its icon.
func (r *renderer) weatherLayout(colW int) weatherSpots {
	down := r.tallerBy() / 4
	grow := r.tallerBy() / 2
	// The lines under one another are a line of their words apart, so larger words keep them apart.
	g := func(px int) int { return r.grownForText(r.s(px)) }
	at := weatherSpots{
		// At 150 across on 190 a storm's bolt hung to within a pixel or two of the condition under it
		// and its rain reached for the reading: a little smaller and higher, it clears both.
		icon:     image.Pt(r.margin+r.s(80), down+r.s(176)+grow/2),
		iconSize: r.s(128),
		reading:  image.Pt(r.margin+r.s(170), down+r.s(225)+grow/2),
		cond:     down + r.s(326) + grow/2,

		boxTop:      down + r.s(88),
		dayIcon:     down + r.s(178) + grow/2,
		dayIconSize: min(colW-r.s(10), r.s(68)+grow/2),
		high:        down + r.s(262) + grow,
	}
	at.rng = at.cond + g(41)
	at.rain = at.rng + g(37)
	at.name = at.boxTop + g(34)
	at.low = at.high + g(38)
	at.perc = at.low + g(38)
	at.boxFoot = at.perc + g(22)
	// Larger words make the boxes taller: they stop short of the Radar button, their figures moving up.
	if over := at.boxFoot - (r.weatherButton().Min.Y - r.s(10)); over > 0 {
		at.dayIcon -= over / 2
		at.high, at.low, at.perc, at.boxFoot = at.high-over, at.low-over, at.perc-over, at.boxFoot-over
	}
	return at
}

func (r *renderer) weatherPage(s scene) {
	r.cornerClock(s)
	r.text(r.small, tr("weather"), r.margin, r.margin+r.s(26), amber)

	days := s.forecast
	now := s.weather
	cond := weatherNow(now, days, s.now)
	// The day's weather, large and faint, behind everything.
	// Sized from the width, not the height: on the Show 5 both give 720, but a taller panel would
	// grow this until it swallowed the page.
	r.faded(40, func() { r.weatherIcon(cond, r.w/2, r.h/2+r.s(20), r.w*3/4) })

	left := r.w/2 + r.s(10)
	cols := max(min(5, len(days)-1), 1)
	colW := (r.w - r.margin - left) / cols
	at := r.weatherLayout(colW)

	// Today: a big icon, the reading beside it, the day's range and rain beneath.
	r.weatherIcon(cond, at.icon.X, at.icon.Y, at.iconSize)
	big := now.Temp
	if big == "" && len(days) > 0 {
		big = fmt.Sprintf("%.0f°", days[0].High)
	}
	r.text(r.big, big, at.reading.X, at.reading.Y, cream)
	// The condition in the sun's pale gold, which a light theme's ground all but swallows: a deeper
	// gold there.
	words := sunPale
	if !dark() {
		words = sunDeep
	}
	r.text(r.body, conditionWords(cond), r.margin, at.cond, words)
	if len(days) > 0 {
		r.text(r.small, weatherRange(days[0].High, days[0].Low), r.margin, at.rng, dim)
		if days[0].Rain >= 0 {
			r.text(r.small, weatherRain(days[0].Rain), r.margin, at.rain, rainInk())
		}
	}

	// The next five days as columns, each with its own icon.
	if len(days) > 1 {
		for i := 0; i < cols; i++ {
			d := days[i+1]
			x := left + i*colW
			// Each day in a filled box with rounded corners, as every other box on the screen is: these
			// were outlines in the rules' color, 1.2:1 on the ground and square, the one page that drew so.
			r.roundRect(image.Rect(x+r.s(3), at.boxTop, x+colW-r.s(3), at.boxFoot), r.s(12), calWash(12))
			name := locale.ShortWeekday(d.When, screenLang())
			if d.When.IsZero() {
				name = fmt.Sprintf("+%d", i+1)
			}
			// The day's name in the quieter ink: the high under it is what the column is for, and in the
			// accent the five names were the loudest thing on the page.
			// Each in the next face down where its own is wider than the column: at 150% on the Skylight
			// the names and the figures ran into the next day's.
			fit := func(f, smaller font.Face, t string) font.Face {
				if r.width(f, t) > colW-r.s(10) {
					return smaller
				}
				return f
			}
			hi := fmt.Sprintf("%.0f°", d.High)
			lo := fmt.Sprintf("%.0f°", d.Low)
			nf, hf, lf := fit(r.small, r.tiny, name), fit(r.body, r.small, hi), fit(r.small, r.tiny, lo)
			r.text(nf, name, x+(colW-r.width(nf, name))/2, at.name, dim)
			r.weatherIcon(d.Condition, x+colW/2, at.dayIcon, at.dayIconSize)
			r.text(hf, hi, x+(colW-r.width(hf, hi))/2, at.high, cream)
			r.text(lf, lo, x+(colW-r.width(lf, lo))/2, at.low, dim)
			if d.Rain > 0 {
				p := fmt.Sprintf("%d%%", d.Rain)
				pf := fit(r.tiny, r.micro, p)
				r.text(pf, p, x+(colW-r.width(pf, p))/2, at.perc, rainInk())
			}
		}
	} else if len(days) == 0 {
		// Wrapped to the room right of today: in one line it ran off the panel's edge.
		msg := "No forecast yet: give the device a Home Assistant token, or set where it is on its setup page"
		x := r.w/2 - r.s(20)
		for i, line := range r.wrap(r.tiny, msg, r.w-r.margin-x) {
			r.text(r.tiny, line, x, at.boxTop+r.s(112)+i*r.s(32), dim)
		}
	}
	r.weatherToggle(tr("ui_radar"))
}

// forecastDays is a type alias for the scene.
type forecastDays = []hass.Day

// radarStep is how long each frame of the rain map's loop shows; the newest holds for three.
const radarStep = 600 * time.Millisecond

// weatherButton is the button at the foot of the weather page that turns between the forecast and
// the rain map.
func (r *renderer) weatherButton() image.Rectangle {
	return image.Rect(r.w-r.margin-r.s(150), r.h-r.s(100), r.w-r.margin, r.h-r.s(52))
}

// weatherToggle is that button drawn, with its label centered in it. The box is scaled along with the
// type: it used to be a fixed 150 wide, so on a Show 8 the label grew and the box did not, and
// "Forecast" ran out of both ends of it.
func (r *renderer) weatherToggle(label string) {
	b := r.weatherButton()
	// The same button as everywhere else, labeled in the sheet's own face: this was a beveled box of
	// this page's own, which is what made one program's buttons look like two.
	col := r.buttonFace(b, float64(b.Dy())/2, btnSecondary)
	face := r.faces().button
	r.text(face, label, b.Min.X+(b.Dx()-r.width(face, label))/2, b.Min.Y+b.Dy()/2+r.s(8), col)
}

// radarPage is the rain map filling the panel, the loop's time and the credits over it, and home
// marked in the middle.
func (r *renderer) radarPage(s scene) {
	v := s.radar
	fillCanvas(r.dst, walnut)
	if len(v.Frames) == 0 {
		r.text(r.small, tr("ui_radar"), r.margin, r.margin+r.s(26), amber)
		msg := tr("loading_the_rain_map")
		if !v.Loading && v.Problem != "" {
			msg = tr("no_rain_map") + v.Problem
		}
		for i, line := range r.wrap(r.body, msg, r.w-2*r.margin) {
			r.text(r.body, line, r.margin, r.s(200)+i*r.s(44), dim)
		}
		r.weatherToggle("Forecast")
		return
	}

	// The loop: each past frame for a step, the newest for three.
	n := len(v.Frames)
	cycle := int64(n+2) * radarStep.Milliseconds()
	i := int(s.now.UnixMilli() % cycle / radarStep.Milliseconds())
	if i >= n {
		i = n - 1
	}
	f := v.Frames[i]
	if f.Image.Rect.Size() == r.dst.Rect.Size() {
		draw.Draw(r.dst, r.dst.Bounds(), f.Image, image.Point{}, draw.Src)
	} else {
		// Made before a panel's screen said its size, and about to be made again at it: scaled for now
		// rather than filling a corner.
		xdraw.ApproxBiLinear.Scale(r.dst, r.dst.Rect, f.Image, cover(f.Image.Rect, r.dst.Rect), draw.Src, nil)
	}

	// The weather alerts' shapes, under everything written over the map.
	alertShapes(r.dst, v, s.alerts.Near, image.Point{}, 1, nil, &r.shapes)
	pillY := r.margin + r.s(42) + r.s(10)
	pills, _, _ := r.alertPills(s.alerts.Here, r.margin, pillY)

	// Home: a ring in the accent with a dark edge, readable over rain and map alike.
	h := v.Home
	r.ring(h, r.s(9), walnut)
	r.ring(h, r.s(7), amber)
	r.ring(h, r.s(5), amber)

	// The towns, where there is room: clear of the bands, the Forecast button and the home marker.
	keep := []image.Rectangle{
		image.Rect(0, 0, r.w, r.margin+r.s(42)),
		image.Rect(0, r.h-r.s(34), r.w, r.h),
		image.Rect(r.w-r.s(200), r.h-r.s(110), r.w, r.h),
		image.Rect(h.X-r.s(12), h.Y-r.s(12), h.X+r.s(12), h.Y+r.s(12)),
	}
	keep = append(keep, pills...)
	if v.Note != "" {
		// The note's line over the credits, as wide as the note draws.
		room := r.weatherButton().Min.X - r.s(12) - r.margin
		keep = append(keep, image.Rect(0, r.h-r.s(62), r.margin+r.width(r.tiny, clipText(r, r.tiny, v.Note, room))+r.s(10), r.h-r.s(34)))
	}
	drawPlaces(r.dst, r.tiny, v.Places, keep, func(b image.Rectangle) bool { return b.In(r.dst.Bounds()) }, 14)

	// The time of the frame, and the clock, on dark bands so they read over the map.
	shade := func(rect image.Rectangle) {
		washOver(r.dst, rect, color.RGBA{0, 0, 0, 150})
	}
	label := "Radar  " + clockText(f.At.Local())
	if i == n-1 {
		label += "  (latest)"
	}
	shade(image.Rect(0, 0, r.w, r.margin+r.s(42)))
	r.text(r.small, label, r.margin, r.margin+r.s(26), amber)
	r.cornerClock(s)
	r.drawAlertPills(s.alerts.Here, r.margin, pillY)

	credit := clipText(r, r.tiny, v.Credit, r.w-2*r.margin)
	shade(image.Rect(0, r.h-r.s(34), r.w, r.h))
	r.text(r.tiny, credit, r.margin, r.h-r.s(10), dim)
	if v.Note != "" {
		// Why the source is not the one chosen: a line of its own over the credits, clear of the button.
		room := r.weatherButton().Min.X - r.s(12) - r.margin
		note := clipText(r, r.tiny, v.Note, room)
		shade(image.Rect(0, r.h-r.s(62), r.margin+r.width(r.tiny, note)+r.s(10), r.h-r.s(34)))
		r.text(r.tiny, note, r.margin, r.h-r.s(40), amber)
	}
	r.weatherToggle("Forecast")
}

// ring is a circle outline one pixel wide.
func (r *renderer) ring(c image.Point, radius int, col color.RGBA) {
	for a := 0; a < 360; a += 2 {
		rad := float64(a) * math.Pi / 180
		x := c.X + int(math.Round(float64(radius)*math.Cos(rad)))
		y := c.Y + int(math.Round(float64(radius)*math.Sin(rad)))
		r.dst.SetRGBA(x, y, col)
	}
}
