//go:build !dot

package display

import (
	"image"
	"image/color"
	"math"
	"strconv"
	"time"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// A drawn dashboard's weather forecast card (#758), as Home Assistant's: the weather now along the
// top, as a tile has it with the temperature large at its end, and the coming days under it side by
// side, each its name, its weather and its high and low, as many as the card's width has room for.
// The days are drawn as a Tessera weather tile draws its own (dayCells).

// dayCell is one day of a forecast as it is drawn: its name, its icon, and its high and low.
type dayCell struct {
	Name, Icon      string
	High, Low       float64
	HasHigh, HasLow bool
}

// Sizes of a forecast card, in the Show 5's pixels.
const (
	forecastNowH  = 84  // the weather now, a tile's height
	forecastDaysH = 118 // the row of days: a name, an icon, a high and a low
)

// forecastCard draws a forecast card at x, y, w wide, and says how tall it is. Off the screen it is
// measured and not drawn. The weather now is a tap of its own, as its tile's would be.
func (r *paint) forecastCard(f dashboard.Forecast, x, y, w int, pal dashPal, tiles *[]dashTile) int {
	cells := forecastCells(f, screenLang())
	now := f.Current || len(cells) == 0 // a card whose days have not come yet still says what it is
	pad := r.s(cardPad)
	h := 0
	if now {
		h += r.s(forecastNowH)
	}
	if len(cells) > 0 {
		h += r.s(forecastDaysH) + pad
		if !now {
			h += pad
		}
	}
	if !r.visible(y, y+h) {
		return h
	}
	r.roundFill(image.Rect(x, y, x+w, y+h), pal.rad, pal.card, pal.card)
	top := y
	if now {
		head := image.Rect(x, y, x+w, y+r.s(forecastNowH))
		r.forecastNow(head, f, pal)
		r.zone(tiles, head, dashboard.Tile{Tap: f.Tap})
		top = head.Max.Y
	} else {
		top += pad
	}
	if len(cells) > 0 {
		r.dayCells(image.Rect(x+pad, top, x+w-pad, top+r.s(forecastDaysH)), cells, false, pal, 1)
	}
	return h
}

// forecastNow is the weather now along the top of a forecast card: the condition's icon in a badge,
// the card's name with the condition under it, and the temperature at the end, large.
func (r *paint) forecastNow(b image.Rectangle, f dashboard.Forecast, pal dashPal) {
	fc := r.faces()
	t := dashboard.Tile{Name: f.Name, Icon: f.Icon, Value: f.Condition, Gone: f.Gone}
	nameInk, valueInk := tileInks(t, pal)
	pad, badge := r.s(12), r.s(44)
	by := b.Min.Y + (b.Dy()-badge)/2
	ring := image.Rect(b.Min.X+pad, by, b.Min.X+pad+badge, by+badge)
	r.tileBadge(ring, t, pal, 26)
	right := b.Max.X - pad - r.s(4)
	mid := b.Min.Y + b.Dy()/2
	if big := r.textFace(false, 40); big != nil && f.Temp != "" {
		tw := r.width(big, f.Temp)
		r.text(big, f.Temp, right-tw, mid+r.s(14), nameInk)
		right -= tw + r.s(12)
	}
	x := ring.Max.X + r.s(12)
	room := right - x
	r.text(fc.label, r.fit(fc.label, f.Name, room), x, mid-r.s(3), nameInk)
	r.text(fc.sub, r.fit(fc.sub, f.Condition, room), x, mid+r.s(24), valueInk)
}

// forecastCells are a card's days as they are drawn, named in the screen's language: the day's short
// name ("Mon"), or the hour for a forecast by the hour. No more than the card asks for are kept.
func forecastCells(f dashboard.Forecast, lang string) []dayCell {
	var out []dayCell
	for _, d := range f.Days {
		if f.Max > 0 && len(out) >= f.Max {
			break
		}
		c := dayCell{Icon: dashboard.WeatherIcon(d.Condition), High: d.High, Low: d.Low, HasHigh: d.HasHigh, HasLow: d.HasLow}
		when := d.When.In(time.Local)
		if f.Hourly {
			c.Name = hourName(when)
		} else {
			c.Name = locale.ShortWeekday(when, lang)
		}
		out = append(out, c)
	}
	return out
}

// hourName is an hour of a forecast by the hour: "3 PM", or "15:00" on a 24-hour clock.
func hourName(t time.Time) string {
	if clock24.Load() {
		return t.Format("15:04")
	}
	return t.Format("3 PM")
}

// daysAcross is how many days side by side a forecast w wide has room for, at k a tile's scale: a
// narrow card has fewer.
func (r *paint) daysAcross(w int, k float64) int {
	return w / max(r.s(int(62*k+0.5)), 1)
}

// dayCells draws a forecast in b, as many days as fit: side by side, each its name, its weather and its
// high and low, or one a line when down is set. k is the tile's scale.
func (r *paint) dayCells(b image.Rectangle, days []dayCell, down bool, pal dashPal, k float64) {
	sc := func(n int) int { return int(float64(n)*k + 0.5) }
	name, hi := r.textFace(false, sc(18)), r.textFace(true, sc(20))
	if name == nil || hi == nil || b.Dx() <= 0 || b.Dy() <= 0 {
		return
	}
	temp := func(d dayCell) (string, string) {
		h, l := "", ""
		if d.HasHigh {
			h = strconv.FormatFloat(math.Round(d.High), 'f', 0, 64) + "°"
		}
		if d.HasLow {
			l = strconv.FormatFloat(math.Round(d.Low), 'f', 0, 64) + "°"
		}
		return h, l
	}
	if down {
		// A line a day, spread over the room up to half as far apart again.
		line := r.s(sc(40))
		n := min(len(days), b.Dy()/max(line, 1))
		if n > 0 {
			line = min(b.Dy()/n, r.s(sc(60)))
		}
		// The names in a column as wide as the widest, the temperatures at the right, and the weather
		// in the middle of what is between.
		names := 0
		for _, d := range days[:n] {
			names = max(names, r.width(name, d.Name))
		}
		names = min(names, b.Dx()/3)
		for i, d := range days[:n] {
			y := b.Min.Y + i*line
			base := y + line/2 + r.s(sc(7))
			r.text(name, r.fit(name, d.Name, names), b.Min.X, base, pal.sub)
			h, l := temp(d)
			x := b.Max.X
			if l != "" {
				x -= r.width(name, l)
				r.text(name, l, x, base, pal.sub)
				x -= r.s(sc(8))
			}
			x -= r.width(hi, h)
			r.text(hi, h, x, base, pal.text)
			if gap := image.Rect(b.Min.X+names, y, x, y+line).Inset(r.s(sc(4))); gap.Dx() >= r.s(sc(20)) {
				// At most two thirds of the gap, in the Show 5's pixels that icon sizes are in.
				r.mdiIconIn(d.Icon, gap, min(sc(26), gap.Dx()*66/max(r.s(100), 1)), pal.text)
			}
		}
		return
	}
	n := min(len(days), r.daysAcross(b.Dx(), k))
	if n == 0 {
		return
	}
	col := b.Dx() / n
	icon := min(b.Dy()/2, r.s(sc(40)), b.Dy()-r.s(sc(22))-r.s(sc(44)))
	if icon < r.s(sc(16)) {
		return // no room for a day's weather
	}
	top := b.Min.Y + (b.Dy()-(r.s(sc(22))+icon+r.s(sc(44))))/2
	for i, d := range days[:n] {
		x := b.Min.X + i*col
		center := func(face font.Face, s string, y int, c color.RGBA) {
			s = r.fit(face, s, col)
			r.text(face, s, x+(col-r.width(face, s))/2, y, c)
		}
		center(name, d.Name, top+r.s(sc(18)), pal.sub)
		r.mdiIconIn(d.Icon, image.Rect(x, top+r.s(sc(22)), x+col, top+r.s(sc(22))+icon), sc(30), pal.text)
		h, l := temp(d)
		center(hi, h, top+r.s(sc(22))+icon+r.s(sc(20)), pal.text)
		center(name, l, top+r.s(sc(22))+icon+r.s(sc(42)), pal.sub)
	}
}
