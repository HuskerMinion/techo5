//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// weatherShow is how long the weather page stays after the question that brought it up.
const weatherShow = 30 * time.Second

// weatherPage is today large on the left and the next days as columns on the right.
func (r *renderer) weatherPage(s scene) {
	r.cornerClock(s)
	r.text(r.small, "Weather", r.margin, r.margin+r.s(26), amber)

	days := s.forecast
	now := s.weather
	cond := now.Condition
	if cond == "" && len(days) > 0 {
		cond = days[0].Condition
	}
	// The day's weather, large and faint, behind everything.
	// Sized from the width, not the height: on the Show 5 both give 720, but a taller panel would
	// grow this until it swallowed the page.
	r.faded(40, func() { r.weatherIcon(cond, r.w/2, r.h/2+r.s(20), r.w*3/4) })

	// Today: a big icon, the reading beside it, the day's range and rain beneath.
	r.weatherIcon(cond, r.margin+r.s(80), r.s(190), r.s(150))
	big := now.Temp
	if big == "" && len(days) > 0 {
		big = fmt.Sprintf("%.0f°", days[0].High)
	}
	r.text(r.big, big, r.margin+r.s(170), r.s(225), cream)
	r.text(r.body, conditionWords(cond), r.margin, r.s(300), sunPale)
	if len(days) > 0 {
		r.text(r.small, fmt.Sprintf("High %.0f°   Low %.0f°", days[0].High, days[0].Low), r.margin, r.s(345), dim)
		if days[0].Rain >= 0 {
			r.text(r.small, fmt.Sprintf("Rain %d%%", days[0].Rain), r.margin, r.s(385), rainBlue)
		}
	}

	// The next five days as columns, each with its own icon.
	if len(days) > 1 {
		left := r.w/2 + r.s(10)
		cols := min(5, len(days)-1)
		colW := (r.w - r.margin - left) / cols
		for i := 0; i < cols; i++ {
			d := days[i+1]
			x := left + i*colW
			// Each day in its own box.
			r.box(image.Rect(x+r.s(3), r.s(88), x+colW-r.s(3), r.s(360)), ember, r.s(2))
			name := d.When.Format("Mon")
			if d.When.IsZero() {
				name = fmt.Sprintf("+%d", i+1)
			}
			r.text(r.small, name, x+(colW-r.width(r.small, name))/2, r.s(122), amber)
			r.weatherIcon(d.Condition, x+colW/2, r.s(178), min(colW-r.s(10), r.s(68)))
			hi := fmt.Sprintf("%.0f°", d.High)
			lo := fmt.Sprintf("%.0f°", d.Low)
			r.text(r.body, hi, x+(colW-r.width(r.body, hi))/2, r.s(262), cream)
			r.text(r.small, lo, x+(colW-r.width(r.small, lo))/2, r.s(300), dim)
			if d.Rain > 0 {
				p := fmt.Sprintf("%d%%", d.Rain)
				r.text(r.tiny, p, x+(colW-r.width(r.tiny, p))/2, r.s(338), rainBlue)
			}
		}
	} else if len(days) == 0 {
		msg := "No forecast yet: call the home_assistant action with a token"
		r.text(r.tiny, msg, r.w/2-r.s(20), r.s(200), dim)
	}
	r.weatherToggle("Radar")
}

// shortCondition is a word that fits a column.
func shortCondition(c string) string {
	switch c {
	case "partlycloudy":
		return "Part cloudy"
	case "lightning-rainy", "lightning":
		return "Storms"
	case "clear-night":
		return "Clear"
	case "pouring":
		return "Heavy rain"
	}
	return conditionWords(c)
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

// weatherToggle is that button drawn, with its label centred in it. The box is scaled along with the
// type: it used to be a fixed 150 wide, so on a Show 8 the label grew and the box did not, and
// "Forecast" ran out of both ends of it.
func (r *renderer) weatherToggle(label string) {
	b := r.weatherButton()
	r.bevel(b, shift(ember, 16), true)
	r.text(r.small, label, b.Min.X+(b.Dx()-r.width(r.small, label))/2, b.Max.Y-r.s(15), cream)
}

// radarPage is the rain map filling the panel, the loop's time and the credits over it, and home
// marked in the middle.
func (r *renderer) radarPage(s scene) {
	v := s.radar
	draw.Draw(r.dst, r.dst.Bounds(), image.NewUniform(walnut), image.Point{}, draw.Src)
	if len(v.Frames) == 0 {
		r.text(r.small, "Radar", r.margin, r.margin+r.s(26), amber)
		msg := "Loading the rain map…"
		if !v.Loading && v.Problem != "" {
			msg = "No rain map: " + v.Problem
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
	draw.Draw(r.dst, r.dst.Bounds(), f.Image, image.Point{}, draw.Src)

	// Home: a ring in the accent with a dark edge, readable over rain and map alike.
	h := v.Home
	r.ring(h, r.s(9), walnut)
	r.ring(h, r.s(7), amber)
	r.ring(h, r.s(5), amber)

	// The time of the frame, and the clock, on dark bands so they read over the map.
	shade := func(rect image.Rectangle) {
		draw.Draw(r.dst, rect, image.NewUniform(color.RGBA{0, 0, 0, 150}), image.Point{}, draw.Over)
	}
	label := "Radar  " + clockText(f.At.Local())
	if i == n-1 {
		label += "  (latest)"
	}
	shade(image.Rect(0, 0, r.w, r.margin+r.s(42)))
	r.text(r.small, label, r.margin, r.margin+r.s(26), amber)
	r.cornerClock(s)

	credit := "Radar RainViewer  ·  Map © OpenStreetMap contributors"
	shade(image.Rect(0, r.h-r.s(34), r.w, r.h))
	r.text(r.tiny, credit, r.margin, r.h-r.s(10), dim)
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
