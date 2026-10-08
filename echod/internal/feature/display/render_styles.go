//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// The Show's clock styles other than the classic face (clock_style.go). Each fills a box: the space
// between the weather corner and whatever takes the foot of the screen - the running timers, the
// glance strip, the music strip, the footer's words - which stay where they always are.

// styleFont is the two faces the styles are cut from, parsed once.
var styleFont struct {
	once          sync.Once
	bold, regular *opentype.Font
}

// styleFaceKey is one face: bold or not, at a size in this panel's own pixels.
type styleFaceKey struct {
	bold bool
	size int
}

// styleFace is a face at size, in the Show 5's pixels and scaled to this panel. Sizes are rounded to a
// few pixels, so a style that fits its text to the box keeps a handful of faces rather than one per
// fraction of a pixel.
func (r *renderer) styleFace(bold bool, size int) font.Face {
	styleFont.once.Do(func() {
		styleFont.bold, _ = opentype.Parse(gobold.TTF)
		styleFont.regular, _ = opentype.Parse(goregular.TTF)
	})
	px := max(r.s(size)/4*4, 8)
	k := styleFaceKey{bold, px}
	if f, ok := r.styleFaces[k]; ok {
		return f
	}
	src := styleFont.regular
	if bold {
		src = styleFont.bold
	}
	if src == nil {
		return r.small
	}
	f, err := opentype.NewFace(src, &opentype.FaceOptions{Size: float64(px), DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		slog.Error("making a clock style's face failed", "size", px, "err", err)
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
	timers := false
	for _, t := range s.timers {
		timers = timers || t.Active
	}
	glance := len(s.glance) > 0 && !timers && !s.strip
	bottom := r.h - r.s(50) // clear of the footer's words
	switch {
	case s.strip:
		bottom = r.stripRect().Min.Y - r.s(14)
	case glance:
		bottom = r.h - r.s(50) - r.s(46) - r.s(16)
	}
	if style == styleGlow {
		r.glowGround(s)
	}
	if timers {
		r.timersLine(s, bottom-r.s(16))
		bottom -= r.s(64)
	}
	box := image.Rect(r.margin, r.s(84), r.w-r.margin, bottom)

	corner := true
	switch style {
	case styleBig:
		r.bigStyle(s, box)
	case styleFlip:
		r.ink = r.dayInk()
		r.flipClock(s.now, image.Rect(box.Min.X, box.Min.Y, box.Max.X, box.Max.Y-r.s(56)))
		r.styleDate(s, r.w/2, box.Max.Y-r.s(8), 0)
	case styleLED:
		r.ink = r.dayInk()
		digits := image.Rect(box.Min.X, box.Min.Y+r.s(16), box.Max.X, box.Max.Y-r.s(76))
		r.ledClock(s.now, digits, r.ampm)
		r.styleDate(s, r.w/2, box.Max.Y-r.s(8), 0)
	case styleAnalog:
		r.analogStyle(s, box)
		corner = false
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
	}
	if glance {
		r.glanceStrip(s.glance, s.callButton)
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

// styleDate is the date line, with the next alarm after it when it is within a day, at x (centered
// on x, or starting there with align -1), and makes it the tap that opens the calendar.
func (r *renderer) styleDate(s scene, x, baseline, align int) {
	r.styleDateIn(r.small, s, x, baseline, align, 0)
}

// styleDateIn is styleDate in face, cut to room when that is more than nothing: the next alarm can
// make the line longer than the space a style has for it.
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
	rad := float64(min(box.Dy()/2+r.s(20), box.Dx()/4))
	cx := float64(box.Min.X) + rad + float64(r.s(8))
	cy := float64(box.Min.Y+box.Dy()/2) - float64(r.s(10))
	r.dial(s.now, cx, cy, rad)

	x := int(cx+rad) + r.s(64)
	mid := box.Min.Y + box.Dy()/2
	day := r.styleFace(true, 56)
	r.text(day, locale.Weekday(s.now, screenLang()), x, mid-r.s(44), cream)
	df := r.styleFace(false, 40)
	date := r.clipTo(df, locale.MonthDay(s.now, screenLang())+alarmSuffix(s), r.w-r.margin-x)
	r.text(df, date, x, mid+r.s(12), dateColor(dim))
	r.setDateAt(image.Rect(x, mid-r.s(96), x+max(r.width(day, locale.Weekday(s.now, screenLang())), r.width(df, date)), mid+r.s(24)))
	if line := weatherText(s); line != "" {
		wy := mid + r.s(82)
		ix := x
		if s.weather.Condition != "" {
			r.weatherIcon(s.weather.Condition, x+weatherMark/2, wy-r.s(12), weatherMark)
			ix += weatherMark + r.s(14)
		}
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
	fit := func(bold bool, size int, text string, w int) font.Face {
		f := r.styleFace(bold, at(size))
		if tw := r.width(f, text); tw > w {
			f = r.styleFace(bold, at(size)*w/tw)
		}
		return f
	}
	small := r.styleFace(false, at(44))
	y := box.Min.Y + r.s(at(44))
	r.text(small, "it's", x, y, dim)
	if lead != "" {
		f := fit(true, 104, lead, room)
		y += capHeight(f) + r.s(at(34))
		r.text(f, lead, x, y, cream)
	}
	hf := fit(true, 124, hour, room*3/5)
	y += capHeight(hf) + r.s(at(34))
	r.text(hf, hour, x, y, dateColor(amber))
	if period != "" {
		pf := fit(false, 44, period, room-r.width(hf, hour)-r.s(36))
		r.text(pf, period, x+r.width(hf, hour)+r.s(30), y, dim)
	}
	r.styleDateIn(r.styleFace(false, at(34)), s, x, y+r.s(at(70)), -1, box.Max.X-x)
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
	r.text(r.tiny, clockText(rise), box.Min.X+r.s(20), int(horizon)+r.s(30), dim)
	st := clockText(set)
	r.text(r.tiny, st, box.Max.X-r.s(20)-r.width(r.tiny, st), int(horizon)+r.s(30), dim)

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
	x := (r.w - r.width(tf, hm) - aw) / 2
	r.text(tf, hm, x, base, cream)
	if ampm != "" {
		r.text(r.title, ampm, x+r.width(tf, hm)+gap, base, dateColor(amber))
	}
	r.styleDate(s, r.w/2, base+r.s(46), 0)
	line := weatherText(s)
	light := sunWords(s.now, rise, set)
	if line != "" && light != "" {
		line += "  ·  "
	}
	line += light
	if line != "" && base+r.s(88) <= box.Max.Y+r.s(20) {
		r.text(r.tiny, line, (r.w-r.width(r.tiny, line))/2, base+r.s(88), dim)
		r.setWeatherAt(image.Rect(r.margin, base+r.s(60), r.w-r.margin, base+r.s(100)))
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
	// The next events, in a column on the right, clear of the date and its alarm.
	col := r.w/2 + r.s(70)
	r.styleDateIn(r.small, s, box.Min.X, base+r.s(50), -1, col-r.s(50)-box.Min.X)
	r.fxLine(float64(col-r.s(30)), float64(top+r.s(4)), float64(col-r.s(30)), float64(base+r.s(56)), float64(r.s(2)), ember, 1)
	r.text(r.styleFace(true, 20), "NEXT", col, top+r.s(20), dim)
	y := top + r.s(62)
	if len(s.style.next) == 0 {
		r.text(r.tiny, "Nothing coming up", col, y, dim)
	}
	tx := col + r.s(150)
	for _, e := range s.style.next {
		tx = max(tx, col+r.width(r.tiny, dashWhen(e, s.now))+r.s(20))
	}
	for _, e := range s.style.next {
		r.text(r.tiny, dashWhen(e, s.now), col, y, amber)
		r.text(r.tiny, r.clipTo(r.tiny, e.Summary, r.w-r.margin-tx), tx, y, cream)
		y += r.s(44)
	}
	// A tap on the column, its heading to its last line, opens the agenda.
	r.setNextAt(image.Rect(col-r.s(20), top-r.s(10), r.w-r.margin, max(y-r.s(30), top+r.s(76))+r.s(10)))

	// The coming days, when there is room for them; otherwise today's weather on a line, so the style that
	// leaves out the weather corner still says what it is outside and still opens the forecast.
	days := s.style.days
	if len(days) == 0 || box.Max.Y-(base+r.s(86)) < r.s(150) { // the row: names, icons and temperatures
		if line := weatherText(s); line != "" {
			y := min(base+r.s(100), box.Max.Y)
			ix := box.Min.X
			if s.weather.Condition != "" {
				r.weatherIcon(s.weather.Condition, ix+weatherMark/2, y-r.s(12), weatherMark)
				ix += weatherMark + r.s(14)
			}
			r.text(r.small, line, ix, y, dim)
			r.setWeatherAt(image.Rect(box.Min.X, y-r.s(40), ix+r.width(r.small, line), y+r.s(14)))
		}
		return
	}
	days = days[:min(len(days), 5)]
	// The row sits at the foot, so the middle of the screen is left clear for a tap that starts Assist.
	row := max(base+r.s(86), box.Max.Y-r.s(150))
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

// dashWhen is when an event starts, as the dashboard writes it: "Now" for one under way, its time
// today, "Tmrw" and the time tomorrow, the weekday and the time after that, and "Today", "Tomorrow" or
// the weekday alone for one with no time.
func dashWhen(e hass.Event, now time.Time) string {
	start, allDay := e.Start, e.AllDay
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	tomorrow, later := today.AddDate(0, 0, 1), today.AddDate(0, 0, 2)
	switch {
	case !allDay && !start.After(now):
		return "Now"
	case allDay && start.Before(tomorrow):
		return "Today"
	case allDay && start.Before(later):
		return "Tomorrow"
	case allDay:
		return locale.ShortWeekday(start, screenLang())
	case start.Before(tomorrow):
		return clockText(start)
	case start.Before(later):
		return "Tmrw " + strings.TrimSpace(clockText(start))
	}
	return locale.ShortWeekday(start, screenLang()) + " " + strings.TrimSpace(clockText(start))
}
