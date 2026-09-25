//go:build spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The weather on the round screen: the temperature and the sky under the clock, and a weather face
// (from the dial, or after a question about the weather) with today large in the upper half and the
// next four days in a row across the lower one. The reading comes from Home Assistant's state, the
// forecast from its REST API, which needs a token (the home_assistant action), as on the Show.

const (
	// weatherShow is how long the weather face stays after a question brought it up; weatherIdle how
	// long it stays when opened from the dial and not touched.
	weatherShow = 30 * time.Second
	weatherIdle = 20 * time.Second
)

var (
	colSun   = color.RGBA{255, 196, 64, 255}
	colCloud = color.RGBA{196, 204, 216, 255}
	colDark  = color.RGBA{128, 138, 156, 255}
	colRain  = color.RGBA{88, 170, 255, 255}
	colSnow  = color.RGBA{230, 240, 255, 255}
	colBolt  = color.RGBA{255, 214, 64, 255}
)

// conditionWords is home.ConditionWords, by its old name here.
func conditionWords(c string) string { return home.ConditionWords(c) }

// weatherLine is the reading under the clock: "72° Partly cloudy", or nothing without one.
func weatherLine(w home.Weather) string {
	words := conditionWords(w.Condition)
	switch {
	case w.Temp != "" && words != "":
		return w.Temp + "  " + words
	case w.Temp != "":
		return w.Temp
	}
	return words
}

// clockWeather draws the reading on the clock face, its icon before it, centered on baseline.
func (r *roundRenderer) clockWeather(w home.Weather, baseline int) {
	line := weatherLine(w)
	if line == "" {
		return
	}
	const u, gap = 15.0, 12
	tw := r.width(r.body, line)
	left := center - (tw+int(2*u)+gap)/2
	r.weatherIcon(w.Condition, float64(left)+u, float64(baseline)-9, u)
	r.over.note(image.Rect(left, baseline-9-int(u), left+int(2*u), baseline-9+int(u)))
	r.text(r.body, line, left+int(2*u)+gap, baseline, colText)
}

// weatherFace is today large and the next days along the bottom.
// weatherFace draws the weather face, and says where a thunderstorm's bolt can strike clear of the
// reading: beside it on whichever side has room inside the circle, or nowhere.
func (r *roundRenderer) weatherFace(s roundScene) (bolt image.Rectangle) {
	r.clear()
	days := s.forecast
	cond := s.weather.Condition
	if cond == "" && len(days) > 0 {
		cond = days[0].Condition
	}
	big := s.weather.Temp
	if big == "" && len(days) > 0 {
		big = fmt.Sprintf("%.0f°", days[0].High)
	}

	r.centered(r.label, clockHM(s.now), 62, colDim)
	if cond == "" && big == "" {
		r.weatherIcon("cloudy", center, 170, 44)
		r.centered(r.title, "No weather yet", 272, colText)
		r.paragraph(r.small, "Home Assistant has not sent a reading", 310, colDim, 2)
		return image.Rectangle{}
	}

	// Today: the icon left of the reading, the words and the day's range under them.
	tw := r.width(r.clock, big)
	const iconU = 44.0
	left := center - (tw+int(2*iconU)+16)/2
	r.weatherIcon(cond, float64(left)+iconU, 150, iconU)
	r.text(r.clock, big, left+int(2*iconU)+16, 190, colText)
	// The bolt goes beside the reading, between y 96 and 214, where the circle is widest: its edge at
	// y 96, the narrowest point of that band, is edge from the middle. A reading too wide for one each
	// side leaves the storm without a bolt rather than striking through the words.
	right := left + int(2*iconU) + 16 + tw
	edge := int(math.Sqrt(float64(center*center - (center-96)*(center-96))))
	switch {
	case right+12+40 <= center+edge-8:
		bolt = image.Rect(right+12, 96, right+52, 214)
	case left-12-40 >= center-edge+8:
		bolt = image.Rect(left-52, 96, left-12, 214)
	}
	r.centered(r.body, conditionWords(cond), 232, colText)
	if len(days) > 0 {
		today := fmt.Sprintf("High %.0f°  Low %.0f°", days[0].High, days[0].Low)
		if days[0].Rain > 0 {
			today += fmt.Sprintf("  Rain %d%%", days[0].Rain)
		}
		r.centered(r.small, today, 266, colDim)
	}

	next := days
	if len(next) > 0 {
		next = next[1:]
	}
	if len(next) == 0 {
		r.paragraph(r.small, "Forecast needs a Home Assistant token", 330, colDim, 2)
		return
	}
	r.line(110, 290, 370, 290, 1.5, colTrack)
	cols := min(4, len(next))
	for i := 0; i < cols; i++ {
		d := next[i]
		x := center + (2*i-(cols-1))*44
		name := d.When.Format("Mon")
		if d.When.IsZero() {
			name = fmt.Sprintf("+%d", i+1)
		}
		r.text(r.label, name, x-r.width(r.label, name)/2, 324, colDim)
		r.weatherIcon(d.Condition, float64(x), 352, 16)
		hi := fmt.Sprintf("%.0f°", d.High)
		lo := fmt.Sprintf("%.0f°", d.Low)
		r.text(r.label, hi, x-r.width(r.label, hi)/2, 396, colText)
		r.text(r.label, lo, x-r.width(r.label, lo)/2, 420, colDim)
	}
	return bolt
}

// weatherIcon draws Home Assistant's condition as a small picture, centered at x, y, u half its size.
func (r *roundRenderer) weatherIcon(cond string, x, y, u float64) {
	w := math.Max(u*0.14, 2)
	switch cond {
	case "sunny":
		r.sunDisc(x, y, u)
	case "clear-night":
		r.moonIcon(x, y, u, w, colSnow)
	case "partlycloudy":
		r.sunDisc(x+0.3*u, y-0.3*u, 0.7*u)
		r.cloud(x-0.1*u, y+0.15*u, 0.8*u, colCloud)
	case "cloudy":
		r.cloud(x+0.25*u, y-0.2*u, 0.6*u, colDark)
		r.cloud(x-0.1*u, y+0.1*u, 0.85*u, colCloud)
	case "rainy", "pouring":
		r.cloud(x, y-0.2*u, 0.85*u, colCloud)
		n := 3
		if cond == "pouring" {
			n = 4
		}
		for k := 0; k < n; k++ {
			dx := (float64(k) - float64(n-1)/2) * 0.42 * u
			r.line(x+dx+0.1*u, y+0.45*u, x+dx-0.1*u, y+0.9*u, w, colRain)
		}
	case "snowy", "snowy-rainy", "hail":
		r.cloud(x, y-0.2*u, 0.85*u, colCloud)
		for k := 0; k < 3; k++ {
			dx := (float64(k) - 1) * 0.45 * u
			c := colSnow
			if cond == "snowy-rainy" && k == 1 {
				r.line(x+dx+0.1*u, y+0.45*u, x+dx-0.1*u, y+0.9*u, w, colRain)
				continue
			}
			r.discAt(x+dx, y+0.55*u+float64(k%2)*0.25*u, math.Max(0.1*u, 1.5), c)
		}
	case "lightning", "lightning-rainy", "exceptional":
		r.cloud(x, y-0.25*u, 0.85*u, colDark)
		r.triangle(x+0.15*u, y+0.1*u, x-0.3*u, y+0.55*u, x+0.05*u, y+0.55*u, colBolt)
		r.triangle(x+0.1*u, y+0.45*u, x-0.1*u, y+1.0*u, x+0.35*u, y+0.4*u, colBolt)
		if cond == "lightning-rainy" {
			r.line(x-0.55*u, y+0.45*u, x-0.7*u, y+0.8*u, w, colRain)
			r.line(x+0.6*u, y+0.45*u, x+0.45*u, y+0.8*u, w, colRain)
		}
	case "fog":
		for k := 0; k < 4; k++ {
			yy := y - 0.6*u + float64(k)*0.4*u
			off := 0.15 * u * float64(k%2)
			r.line(x-0.8*u+off, yy, x+0.8*u+off-0.3*u, yy, w*1.3, colCloud)
		}
	case "windy", "windy-variant":
		for k, l := range []float64{0.9, 1.3, 0.7} {
			yy := y - 0.45*u + float64(k)*0.45*u
			r.line(x-0.85*u, yy, x-0.85*u+l*u, yy, w*1.2, colCloud)
			r.ringAt(x-0.85*u+l*u, yy-0.2*u, 0.2*u-w*0.6, 0.2*u+w*0.6, 0, math.Pi, colCloud)
		}
	case "":
	default:
		r.cloud(x, y, 0.85*u, colCloud)
	}
}

// sunDisc is a filled sun with short rays.
func (r *roundRenderer) sunDisc(x, y, u float64) {
	r.discAt(x, y, 0.45*u, colSun)
	w := math.Max(u*0.12, 1.8)
	for k := 0; k < 8; k++ {
		a := float64(k) * math.Pi / 4
		r.line(x+0.64*u*math.Sin(a), y-0.64*u*math.Cos(a), x+0.92*u*math.Sin(a), y-0.92*u*math.Cos(a), w, colSun)
	}
}

// cloud is a filled cloud, u its half width, its flat base a little below y.
func (r *roundRenderer) cloud(x, y, u float64, c color.RGBA) {
	r.discAt(x-0.55*u, y+0.12*u, 0.38*u, c)
	r.discAt(x-0.05*u, y-0.18*u, 0.52*u, c)
	r.discAt(x+0.55*u, y+0.1*u, 0.4*u, c)
	r.line(x-0.55*u, y+0.3*u, x+0.55*u, y+0.3*u, 0.4*u, c)
}

// forecastDays is the scene's copy of the forecast.
type forecastDays = []hass.Day

// dateWeather is the line under the time while something is playing: the day, and the temperature
// with its icon when there is one. The face has no corner to put the weather in the way the Show
// does, and no room between the date and the picture for a line of its own, so the two share one —
// which is also the reading somebody glances at, rather than the words for it.
func (r *roundRenderer) dateWeather(w home.Weather, when time.Time, baseline int) {
	if w.Temp == "" {
		r.centered(r.small, when.Format("Monday, January 2"), baseline, colDim)
		return
	}
	// Sharing the line costs the long day and month: written out, the two together reach the bezel,
	// and a round screen has less room the further from the middle a line sits.
	const u, gap = 11.0, 8
	line := w.Temp + "  ·  " + when.Format("Mon, Jan 2")
	tw := r.width(r.small, line)
	icon := 0
	if conditionWords(w.Condition) != "" {
		icon = int(2*u) + gap
	}
	left := center - (tw+icon)/2
	if icon > 0 {
		r.weatherIcon(w.Condition, float64(left)+u, float64(baseline)-7, u)
	}
	r.text(r.small, line, left+icon, baseline, colDim)
}
