//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"time"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// The Show's clock styles other than the classic face (clock_style.go). Each fills a box: the space
// between the weather corner and whatever takes the foot of the screen - the running timers, the
// glance strip, the music strip, the footer's words - which stay where they always are.

// styleFaceKey is one face: bold or not, at a size in this panel's own pixels.
type styleFaceKey struct {
	bold bool
	size int
}

// styleFace is a face at size in the screen font, in the Show 5's pixels and scaled to this panel.
// Sizes are rounded to a few pixels, so a style that fits its text to the box keeps a handful of faces
// rather than one per fraction of a pixel. A new font empties the set (useFont).
func (r *renderer) styleFace(bold bool, size int) font.Face {
	px := max(r.s(size)/4*4, 8)
	k := styleFaceKey{bold, px}
	if f, ok := r.styleFaces[k]; ok {
		return f
	}
	f := fontFace(r.font, bold, float64(px))
	if f == nil {
		return r.small
	}
	if r.styleFaces == nil || len(r.styleFaces) > 24 {
		r.styleFaces = map[styleFaceKey]font.Face{}
	}
	r.styleFaces[k] = f
	return f
}

// capHeight is how tall face's figures stand above the baseline.
func capHeight(face font.Face) int {
	if b, _, ok := face.GlyphBounds('8'); ok {
		return (-b.Min.Y).Ceil()
	}
	return face.Metrics().Ascent.Ceil() * 7 / 10
}

// styledClock is the home screen in a clock style other than the classic one.
func (r *renderer) styledClock(s scene, style string) {
	timers := timersAtFoot(s)
	// The next-up card, in place of the glance strip, unless the style lists the events itself or the clock
	// is in a bottom corner where the card would be.
	_, foot := clockAlign()
	card := s.nextUp != nil && len(s.glance) == 0 && !timers && !s.strip && !(foot && cornerStyle(style))
	glance := len(s.glance) > 0 && !timers && !s.strip && !card
	bottom := r.h - r.s(50) // clear of the footer's words
	switch {
	case s.strip:
		bottom = r.stripRect().Min.Y - r.s(14)
	case card:
		box, _ := r.nextUpRect(s)
		bottom = box.Min.Y - r.s(14)
	case glance:
		bottom = r.h - r.s(50) - r.s(46) - r.s(16)
	}
	if style == styleGlow {
		r.glowGround(s)
		r.skinBackdrop(s)
	}
	if timers {
		r.timersFoot(s, bottom-r.s(16))
		bottom -= r.s(64)
	}
	box := image.Rect(r.margin, r.s(84), r.w-r.margin, bottom)
	r.cornered = false
	if align, foot := clockAlign(); foot && cornerStyle(style) {
		box, r.cornered = r.cornerBox(s, style, align, bottom), true
	}

	corner := true
	switch style {
	case styleBig:
		r.bigStyle(s, box)
	case styleFlip:
		r.ink = r.dayInk()
		r.flipClock(s.now, image.Rect(box.Min.X, box.Min.Y, box.Max.X, box.Max.Y-r.s(56)))
		r.styleDateUnder(s, box, box.Max.Y-r.s(8))
	case styleLED:
		r.ink = r.dayInk()
		if r.cornered {
			// Small, the unlit segments crowd the lit ones: fainter, so the time reads first.
			r.ink.ghost = faded(r.ink.ghost, 96)
		}
		digits := image.Rect(box.Min.X, box.Min.Y+r.s(16), box.Max.X, box.Max.Y-r.s(76))
		r.ledClock(s.now, digits, r.ampm)
		r.styleDateUnder(s, box, box.Max.Y-r.s(8))
	case styleAnalog:
		r.analogStyle(s, box)
		corner = r.cornered // in a corner it leaves the weather to the weather corner
	case styleWords:
		r.wordsStyle(s, box)
	case styleSun:
		corner = !r.sunStyle(s, box)
	case styleDashboard:
		r.dashboardStyle(s, box)
		corner = false
	case styleBinary:
		r.binaryStyle(s, box)
	case styleWorld:
		r.worldStyle(s, box)
	case styleAgenda:
		r.agendaStyle(s, box)
	case styleGlow:
		r.glowStyle(s, box)
	case styleNixie:
		r.nixieStyle(s, box)
	case styleStack:
		r.stackStyle(s, box)
	case styleOverlap:
		r.overlapStyle(s, box)
	}
	if glance {
		r.glanceStrip(s.glance, s.callButton)
	}
	if card {
		r.drawNextUp(s)
	}
	if corner {
		r.weatherCorner(s)
	}
	r.alertBadge(s)
}

// dayInk is the LED and flip clocks in the theme's colors: the accent lit, the ground's own shade for
// the cards.
func (r *renderer) dayInk() clockInk {
	return clockInk{
		lit: amber, ghost: lerp(walnut, amber, 0.09),
		cardTop: shift(walnut, 22), cardLower: shift(walnut, 15),
		hinge: shift(walnut, 40), figure: cream, split: walnut,
	}
}

// styleDateUnder is the date line centered under a style drawn in box (styleDateIn). In a corner the box is narrower than
// a long date, with the next alarm after it, would be: there it is cut to the box, so it neither starts
// off the screen's edge nor runs on over the middle.
func (r *renderer) styleDateUnder(s scene, box image.Rectangle, baseline int) {
	width := 0
	if r.cornered {
		width = box.Dx()
	}
	r.styleDateIn(r.small, s, box.Min.X+box.Dx()/2, baseline, 0, width)
}

// styleDateIn is the date line, with the next alarm after it when it is within a day, at x (centered
// on x, or starting there with align -1), and makes it the tap that opens the calendar. It is cut to
// room when that is more than nothing: the next alarm can make the line longer than the space a style
// has for it.
func (r *renderer) styleDateIn(face font.Face, s scene, x, baseline, align, room int) {
	date := locale.LongDate(s.now, screenLang()) + alarmSuffix(s)
	if room > 0 && r.width(face, date) > room {
		// The day and month short first, so the alarm still fits; cut only if that is not enough.
		date = r.clipTo(face, locale.ShortDate(s.now, screenLang())+alarmSuffix(s), room)
	}
	w := r.width(face, date)
	if align == 0 {
		x -= w / 2
	}
	r.text(face, date, x, baseline, dateColor(dim))
	r.setDateAt(image.Rect(x, baseline-r.s(30), x+w, baseline+r.s(10)).Inset(-r.s(16)))
}

// alarmSuffix is the next alarm, when it is within a day, for after the date.
func alarmSuffix(s scene) string {
	next := s.alarms.Next
	if next == nil || next.At.Sub(s.now) >= 24*time.Hour {
		return ""
	}
	return "  ·  " + locale.Alarm(screenLang(), next.Snoozed) + " " + clockText(next.At)
}

// bigStyle is the time alone, as large as the box allows: for reading across a room.
func (r *renderer) bigStyle(s scene, box image.Rectangle) {
	hm, ampm := clockHM(s.now), clockSuffix(s.now)
	const ref = 400
	f := r.styleFace(true, ref)
	aw := 0
	if ampm != "" {
		aw = r.width(r.ampm, ampm) + r.s(16)
	}
	scale := math.Min(float64(box.Dx()-aw)*0.96/float64(r.width(f, hm)), float64(box.Dy())*0.9/float64(capHeight(f)))
	f = r.styleFace(true, int(ref*scale))
	w, h := r.width(f, hm), capHeight(f)
	x := box.Min.X + (box.Dx()-w-aw)/2
	base := box.Min.Y + (box.Dy()+h)/2
	r.text(f, hm, x, base, cream)
	if ampm != "" {
		r.text(r.ampm, ampm, x+w+r.s(16), base-h+capHeight(r.ampm), dateColor(amber))
	}
	// The whole time opens the calendar, as the date does under the classic face.
	r.setDateAt(image.Rect(x, base-h, x+w+aw, base))
}

// analogStyle is a dial on the left, and the day, the date and the weather beside it.
func (r *renderer) analogStyle(s scene, box image.Rectangle) {
	// In a corner (Clock position) the dial and the words beside it come down together.
	k, rad := 1.0, float64(min(box.Dy()/2+r.s(20), box.Dx()/4))
	if r.cornered {
		k, rad = 0.62, float64(min(box.Dy()/2, box.Dx()/5))
	}
	at := func(n int) int { return int(float64(n) * k) }
	cx := float64(box.Min.X) + rad + float64(r.s(8))
	cy := float64(box.Min.Y+box.Dy()/2) - float64(r.s(10))
	r.dialWedge(s, cx, cy, rad)
	r.dial(s.now, cx, cy, rad)

	x := int(cx+rad) + r.s(at(64))
	mid := box.Min.Y + box.Dy()/2
	day := r.styleFace(true, at(56))
	r.text(day, locale.Weekday(s.now, screenLang()), x, mid-r.s(at(44)), cream)
	df := r.styleFace(false, at(40))
	date := r.clipTo(df, locale.MonthDay(s.now, screenLang())+alarmSuffix(s), box.Max.X-x)
	r.text(df, date, x, mid+r.s(at(12)), dateColor(dim))
	r.setDateAt(image.Rect(x, mid-r.s(at(96)), x+max(r.width(day, locale.Weekday(s.now, screenLang())), r.width(df, date)), mid+r.s(at(24))))
	// In a corner the weather corner at the top has the weather.
	if line := weatherText(s); line != "" && !r.cornered {
		wy := mid + r.s(82)
		ix := x
		if s.weather.Condition != "" {
			mark := r.s(weatherMark)
			r.weatherIcon(s.weather.Condition, x+mark/2, wy-r.s(12), mark)
			ix += mark + r.s(14)
		}
		// Clipped like the date: a long condition (French, Russian) ran off the screen.
		line = r.clipTo(r.small, line, box.Max.X-ix)
		r.text(r.small, line, ix, wy, dim)
		r.setWeatherAt(image.Rect(x, wy-r.s(40), ix+r.width(r.small, line), wy+r.s(14)))
	}
}

// weatherText is the weather's reading and words, as the corner writes them.
func weatherText(s scene) string {
	line := s.weather.Temp
	if c := conditionWords(s.weather.Condition); c != "" {
		if line != "" {
			line += "  ·  "
		}
		line += c
	}
	return line
}

// dial is an analog clock face centered at cx, cy: minute marks, the four quarters numbered, hour and
// minute hands in the text color and a thin second hand in the accent.
func (r *renderer) dial(now time.Time, cx, cy, rad float64) {
	at := func(dist, deg float64) (float64, float64) {
		a := (deg - 90) * math.Pi / 180
		return cx + dist*math.Cos(a), cy + dist*math.Sin(a)
	}
	for i := range 60 {
		long := i%5 == 0
		in, w, c := rad*0.94, rad*0.012, lerp(walnut, cream, 0.35)
		if long {
			in, w, c = rad*0.86, rad*0.024, cream
		}
		x0, y0 := at(rad, float64(i*6))
		x1, y1 := at(in, float64(i*6))
		r.fxLine(x0, y0, x1, y1, max(w, 1.5), c, 1)
	}
	num := r.styleFace(false, int(rad*0.16*float64(drawnFor)/float64(r.w)))
	for n, deg := range map[string]float64{"12": 0, "3": 90, "6": 180, "9": 270} {
		x, y := at(rad*0.7, deg)
		w := r.width(num, n)
		r.text(num, n, int(x)-w/2, int(y)+capHeight(num)/2, dim)
	}
	hand := func(deg, length, back, w float64, c color.RGBA) {
		x0, y0 := at(-back, deg)
		x1, y1 := at(length, deg)
		r.fxLine(x0, y0, x1, y1, w, c, 1)
	}
	h, m, sec := float64(now.Hour()%12), float64(now.Minute()), float64(now.Second())
	hand((h+m/60)*30, rad*0.5, rad*0.08, rad*0.055, cream)
	hand((m+sec/60)*6, rad*0.78, rad*0.08, rad*0.035, cream)
	hand(sec*6, rad*0.84, rad*0.14, max(rad*0.012, 2), amber)
	r.fxDisc(cx, cy, rad*0.045, amber, 1)
	r.over.note(image.Rect(int(cx-rad), int(cy-rad), int(cx+rad), int(cy+rad)))
}

// wordsStyle says the time: "it's seven past two in the afternoon". Everything shrinks together when
// a timer or a strip at the foot leaves it less height than it was drawn for.
func (r *renderer) wordsStyle(s scene, box image.Rectangle) {
	lead, hour, period := clockWords(s.now)
	k := min(1, float64(box.Dy())/float64(r.s(372)))
	at := func(n int) int { return int(float64(n) * k) }
	x := box.Min.X + r.s(24)
	room := box.Dx() - r.s(48)
	// In a corner the small words keep a size that can still be read.
	least := func(size, floor int) int {
		if r.cornered {
			return max(at(size), floor)
		}
		return at(size)
	}
	fit := func(bold bool, size int, text string, w int) font.Face {
		f := r.styleFace(bold, size)
		if tw := r.width(f, text); tw > w {
			f = r.styleFace(bold, size*w/tw)
		}
		return f
	}
	small := r.styleFace(false, least(44, 32))
	y := box.Min.Y + r.s(at(44))
	r.text(small, "it's", x, y, dim)
	if lead != "" {
		f := fit(true, at(104), lead, room)
		y += capHeight(f) + r.s(at(34))
		r.text(f, lead, x, y, cream)
	}
	hf := fit(true, at(124), hour, room*3/5)
	y += capHeight(hf) + r.s(at(34))
	r.text(hf, hour, x, y, dateColor(amber))
	if period != "" {
		pf := fit(false, least(44, 32), period, room-r.width(hf, hour)-r.s(36))
		r.text(pf, period, x+r.width(hf, hour)+r.s(30), y, dim)
	}
	r.styleDateIn(r.styleFace(false, least(34, 28)), s, x, y+r.s(at(70)), -1, box.Max.X-x)
}

// sunStyle is the day's sun: its path from sunrise to sunset across the top, the sun where it is now,
// the time under it. It reports whether it drew the weather itself; until home's place is known it
// is the classic face.
func (r *renderer) sunStyle(s scene, box image.Rectangle) bool {
	rise, set, ok := s.style.rise, s.style.set, s.style.sunOK
	if !ok {
		r.setDateAt(r.timeAndDate(s.now, r.h/2+r.s(60), alarmSuffix(s)).Inset(-r.s(16)))
		return false
	}
	x0, x1 := float64(box.Min.X+r.s(20)), float64(box.Max.X-r.s(20))
	horizon := float64(box.Min.Y) + float64(box.Dy())*0.34
	peak := float64(box.Min.Y)
	day := set.Sub(rise).Seconds()
	at := func(f float64) (float64, float64) {
		return x0 + (x1-x0)*f, horizon - (horizon-peak)*math.Sin(math.Pi*f)
	}
	f := s.now.Sub(rise).Seconds() / day
	const steps = 60
	px, py := at(0)
	for i := 1; i <= steps; i++ {
		fi := float64(i) / steps
		x, y := at(fi)
		c, w := lerp(walnut, amber, 0.35), float64(r.s(4))
		if fi <= f {
			c, w = amber, float64(r.s(6))
		}
		r.fxLine(px, py, x, y, w, c, 1)
		px, py = x, y
	}
	r.fxLine(float64(box.Min.X), horizon, float64(box.Max.X), horizon, float64(r.s(3)), ember, 1)
	switch {
	case f >= 0 && f <= 1:
		sx, sy := at(f)
		r.fxDisc(sx, sy, float64(r.s(26)), lerp(amber, cream, 0.4), 0.45)
		r.fxDisc(sx, sy, float64(r.s(18)), color.RGBA{255, 226, 150, 255}, 1)
	case f < 0:
		r.fxDisc(x0, horizon, float64(r.s(10)), lerp(walnut, amber, 0.5), 1)
	default:
		r.fxDisc(x1, horizon, float64(r.s(10)), lerp(walnut, amber, 0.5), 1)
	}
	if !r.cornered { // in a corner too small to read: the arc says it alone
		r.text(r.tiny, clockText(rise), box.Min.X+r.s(20), int(horizon)+r.s(30), dim)
		st := clockText(set)
		r.text(r.tiny, st, box.Max.X-r.s(20)-r.width(r.tiny, st), int(horizon)+r.s(30), dim)
	}

	// The time and date under the horizon, the weather and the light left under those.
	hm, ampm := clockHM(s.now), clockSuffix(s.now)
	// The time as large as leaves the date inside the box: smaller when a timer or a strip takes the
	// foot of the screen.
	size := 118
	tf := r.styleFace(true, size)
	for size > 44 && int(horizon)+r.s(40)+capHeight(tf)+r.s(46) > box.Max.Y+r.s(6) {
		size -= 12
		tf = r.styleFace(true, size)
	}
	gap := r.s(14)
	aw := 0
	if ampm != "" {
		aw = r.width(r.title, ampm) + gap
	}
	base := int(horizon) + r.s(40) + capHeight(tf)
	x := box.Min.X + (box.Dx()-r.width(tf, hm)-aw)/2
	r.text(tf, hm, x, base, cream)
	if ampm != "" {
		r.text(r.title, ampm, x+r.width(tf, hm)+gap, base, dateColor(amber))
	}
	r.styleDateUnder(s, box, base+r.s(46))
	line := weatherText(s)
	light := sunWords(s.now, rise, set)
	if line != "" && light != "" {
		line += "  ·  "
	}
	line += light
	if line != "" && base+r.s(88) <= box.Max.Y+r.s(20) {
		r.text(r.tiny, line, box.Min.X+(box.Dx()-r.width(r.tiny, line))/2, base+r.s(88), dim)
		r.setWeatherAt(image.Rect(box.Min.X, base+r.s(60), box.Max.X, base+r.s(100)))
	}
	return true
}

// sunWords is what is left of the light, or when it comes back.
func sunWords(now, rise, set time.Time) string {
	switch {
	case now.Before(rise):
		return "Sunrise at " + clockText(rise)
	case now.Before(set):
		left := set.Sub(now).Round(time.Minute)
		return fmt.Sprintf("%d h %d m of daylight left", int(left.Hours()), int(left.Minutes())%60)
	}
	return "Sunset was at " + clockText(set)
}

// dashboardStyle is the time with the day's next events beside it and the coming days' weather along
// the foot.
func (r *renderer) dashboardStyle(s scene, box image.Rectangle) {
	hm, ampm := clockHM(s.now), clockSuffix(s.now)
	tf := r.styleFace(true, 132)
	top := box.Min.Y - r.s(40) // the weather corner is not drawn: the top is the time's
	base := top + capHeight(tf) + r.s(8)
	r.text(tf, hm, box.Min.X, base, cream)
	if ampm != "" {
		r.text(r.title, ampm, box.Min.X+r.width(tf, hm)+r.s(12), base, dateColor(amber))
	}
	// The coming days, when there is room for them, sit at the foot; the events above them get whatever
	// height is left, as many as fit.
	days, rowH := s.style.days, r.s(150) // the row: names, icons and temperatures
	showRow := len(days) > 0 && box.Max.Y-(base+r.s(86)) >= rowH
	row, evEnd := 0, box.Max.Y
	if showRow {
		// The row sits at the foot, so the middle of the screen is left clear for a tap that starts Assist.
		row = max(base+r.s(86), box.Max.Y-rowH)
		evEnd = row - r.s(24)
	}
	// Across, everything is placed from the widest the time and the temperature can be, not this
	// minute's or this reading's, so nothing moves as they change: the time's digits are all one width,
	// so "88:88" and the wider of AM and PM, and a reading is at most "-88°" or "888°".
	hf, mark := r.styleFace(true, 40), r.s(48)
	timeW := r.width(tf, "88:88")
	if ampm != "" {
		timeW += r.s(12) + max(r.width(r.title, "AM"), r.width(r.title, "PM"))
	}
	tempX, tempW := box.Min.X+timeW+r.s(28), max(mark, r.width(hf, "-88°"), r.width(hf, "888°"))
	// The next events, in a column right of the middle, which is left to a tap that starts Assist: a
	// steady margin past the time and the temperature, as much air either side of the divider at col-30.
	col := min(max(tempX+tempW+r.s(60), r.w/2+r.s(40)), r.w/2+r.s(70))
	// The temperature now, while the row of the coming days, which only has their highs and lows, is
	// up; without the row, the weather line under the date has it. Beside the time, the icon over the
	// degrees, where that always fits; otherwise at the end of the events' heading. A tap on it opens the
	// forecast, as one on the row does.
	if t := s.weather.Temp; t != "" && showRow {
		if tempX+tempW <= col-r.s(40) {
			cx := tempX + tempW/2
			if s.weather.Condition != "" {
				r.weatherIcon(s.weather.Condition, cx, top+r.s(14)+mark/2, mark)
			}
			r.text(hf, t, cx-r.width(hf, t)/2, base, cream)
			r.setTempAt(image.Rect(tempX, top+r.s(14), tempX+tempW, base).Inset(-r.s(10)))
		} else {
			hf, mark = r.styleFace(true, 30), r.s(40)
			x := r.w - r.margin - r.width(hf, t)
			r.text(hf, t, x, top+r.s(24), cream)
			if s.weather.Condition != "" {
				r.weatherIcon(s.weather.Condition, x-r.s(10)-mark/2, top+r.s(24)-capHeight(hf)/2, mark)
			}
			wide := max(r.width(hf, "-88°"), r.width(hf, "888°"))
			r.setTempAt(image.Rect(r.w-r.margin-wide-r.s(10)-mark, top, r.w-r.margin, top+r.s(34)))
		}
	}
	r.styleDateIn(r.small, s, box.Min.X, base+r.s(50), -1, col-r.s(50)-box.Min.X)
	r.text(r.styleFace(true, 20), locale.Upcoming(screenLang()), col, top+r.s(20), dim)
	y := top + r.s(62)
	if len(s.style.next) == 0 {
		r.text(r.tiny, locale.NothingSoon(screenLang()), col, y, dim)
	}
	// As many events as fit above the row, or the foot, a line each.
	shown, fit := s.style.next, 0
	if evEnd >= y {
		fit = (evEnd-y)/r.s(44) + 1
	}
	shown = shown[:min(len(shown), fit)]
	// Each "when" is its day in the accent and its time beside it, quieter; the names start just past
	// the widest of those shown, so short ones ("Sun") leave the names their room.
	whenWidth := func(day, hhm string) int {
		w := r.width(r.tiny, day)
		if hhm != "" {
			w += r.s(8) + r.width(r.tiny, hhm)
		}
		return w
	}
	// A long "when" in another language on a 12-hour clock ("Aujourd'hui 11:30 PM") left the names a
	// few letters; past dashWhenShare of the column the times lose their AM/PM instead.
	short := false
	widest := func() int {
		tx := col
		for _, e := range shown {
			day, hhm := dashWhen(e, s.now)
			if short {
				hhm = dashBareTime(hhm)
			}
			tx = max(tx, col+whenWidth(day, hhm)+r.s(18))
		}
		return tx
	}
	tx := widest()
	if tx-col > (r.w-r.margin-col)*dashWhenShare/100 {
		short = true
		tx = widest()
	}
	for _, e := range shown {
		day, hhm := dashWhen(e, s.now)
		if short {
			hhm = dashBareTime(hhm)
		}
		r.text(r.tiny, day, col, y, amber)
		if hhm != "" {
			r.text(r.tiny, hhm, col+r.width(r.tiny, day)+r.s(8), y, dim)
		}
		r.text(r.tiny, r.clipTo(r.tiny, e.Summary, r.w-r.margin-tx), tx, y, cream)
		y += r.s(44)
	}
	// The divider, between the time and the events: past the date and the last event, and down to the
	// row when it is up.
	divEnd := max(base+r.s(56), y-r.s(30))
	if showRow {
		divEnd = max(divEnd, evEnd)
	}
	r.fxLine(float64(col-r.s(30)), float64(top+r.s(4)), float64(col-r.s(30)), float64(divEnd), float64(r.s(2)), ember, 1)
	// A tap on the column, its heading to its last line, opens the agenda.
	r.setNextAt(image.Rect(col-r.s(20), top-r.s(10), r.w-r.margin, max(y-r.s(30), top+r.s(76))+r.s(10)))

	// Without room for the row, today's weather on a line, so the style that leaves out the weather
	// corner still says what it is outside and still opens the forecast.
	if !showRow {
		if line := weatherText(s); line != "" {
			y := min(base+r.s(100), box.Max.Y)
			ix := box.Min.X
			if s.weather.Condition != "" {
				mark := r.s(weatherMark)
				r.weatherIcon(s.weather.Condition, ix+mark/2, y-r.s(12), mark)
				ix += mark + r.s(14)
			}
			// Kept left of the divider, as the date is: a long condition would otherwise run into the
			// events, which go further down without the row.
			line = r.clipTo(r.small, line, col-r.s(50)-ix)
			r.text(r.small, line, ix, y, dim)
			r.setWeatherAt(image.Rect(box.Min.X, y-r.s(40), ix+r.width(r.small, line), y+r.s(14)))
		}
		return
	}
	days = days[:min(len(days), 5)]
	left, right := r.dashRowSpan(days, box, row, s.callButton)
	cw := (right - left) / len(days)
	for i, d := range days {
		cx := left + cw*i + cw/2
		name := locale.ShortWeekday(d.When, screenLang())
		if i == 0 {
			name = locale.Today(screenLang())
		}
		c := dim
		if i == 0 {
			c = cream
		}
		r.text(r.tiny, name, cx-r.width(r.tiny, name)/2, row+r.s(36), c)
		r.weatherIcon(d.Condition, cx, row+r.s(80), r.s(44))
		t := dayTemps(d)
		r.text(r.tiny, t, cx-r.width(r.tiny, t)/2, row+r.s(138), cream)
	}
	r.setWeatherAt(image.Rect(left, row, right, row+r.s(150)))
}

func dayTemps(d hass.Day) string { return fmt.Sprintf("%.0f°  %.0f°", d.High, d.Low) }

// dashRowSpan is where across the row of the coming days is drawn. The Call button has the bottom-left
// corner, over Today's icon and temperatures: while it shows level with the row, the row narrows from
// both sides, staying centered, just enough that Today clears it.
func (r *renderer) dashRowSpan(days []hass.Day, box image.Rectangle, row int, callButton bool) (left, right int) {
	left, right = box.Min.X, box.Max.X
	b := r.callButtonRect()
	n := len(days)
	if !callButton || n < 2 || row+r.s(150) <= b.Min.Y {
		return left, right
	}
	// Today's icon and temperatures, half of the wider, and how far their left edge is short of clear.
	half := max(r.width(r.tiny, dayTemps(days[0])), r.s(44)) / 2
	short := b.Max.X + r.s(12) + half - (left + (right-left)/n/2)
	if short <= 0 {
		return left, right
	}
	// Each step in from both sides moves Today's center right by 1 - 1/n of it.
	in := (short*n + n - 2) / (n - 1)
	return left + in, right - in
}

// dashWhen is when an event starts, as the dashboard writes it, in the screen's language: the day
// ("Today", or the short weekday from tomorrow on) and apart its time, empty for an all-day event; an
// event under way is "Now" with no time.
// dashWhenShare is the most of the Dashboard's event column, in percent, the "when" may take before
// its times drop their AM/PM.
const dashWhenShare = 45

// dashBareTime is a 12-hour time without its AM/PM ("11:30 PM" is "11:30"); a 24-hour one is
// unchanged.
func dashBareTime(hhm string) string {
	if f := strings.Fields(hhm); len(f) > 1 {
		return f[0]
	}
	return hhm
}

func dashWhen(e hass.Event, now time.Time) (day, hhm string) {
	lang := screenLang()
	start := e.Start
	if !e.AllDay && !start.After(now) {
		return locale.Now(lang), ""
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	day = locale.ShortWeekday(start, lang)
	if start.Before(today.AddDate(0, 0, 1)) {
		day = locale.Today(lang)
	}
	if !e.AllDay {
		hhm = strings.TrimSpace(clockText(start))
	}
	return day, hhm
}

// cornerStyle is whether a style has a small version for a corner at the foot (Clock position). The
// Dashboard, the Agenda and World are pages of their own rather than a clock, and keep the whole
// screen.
func cornerStyle(style string) bool {
	return style != styleDashboard && style != styleAgenda && style != styleWorld
}

// cornerPct is the share of the width a style gets in a corner, the most of it the date may take.
const cornerPct = 44

// cornerBox is the box a style is drawn in at the foot, where align says: the left corner (clear of
// the Call button), the middle or the right corner, its foot where the whole screen's box ends. The dial and the binary
// lights are wide for their height, so theirs is narrower, to keep them clear of the middle.
func (r *renderer) cornerBox(s scene, style string, align, bottom int) image.Rectangle {
	pct := cornerPct
	switch style {
	case styleAnalog:
		pct = 40
	case styleBinary:
		pct = 36
	}
	if r.w < 2*r.h {
		// The Show 8's panel is taller for its width, so a style drawn to the box's height comes out
		// wider for it: a smaller share of the width keeps it as far from the middle.
		pct = pct * 39 / 44
	}
	w, h := r.w*pct/100, r.s(190)
	x := r.w - r.margin - w
	if align == 0 {
		x = (r.w - w) / 2
	}
	if align < 0 {
		x = r.margin
		if s.callButton {
			x = r.callButtonRect().Max.X + r.s(24)
		}
	}
	return image.Rect(x, bottom-h, x+w, bottom)
}
