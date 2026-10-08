//go:build spot

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

	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// The Spot's clock styles other than the classic face (clock_style.go), laid out for the circle. The
// rim stays the status ring - listening, muted, a running timer - except that the Sun style draws the
// day's light on it while there is nothing else to say there. The alert pill stays at the top.

// spotStyleFont is the two faces the styles are cut from, parsed once.
var spotStyleFont struct {
	once          sync.Once
	bold, regular *opentype.Font
}

type spotFaceKey struct {
	bold bool
	size int
}

// styleFace is a face at size pixels, kept once made; sizes are rounded to a few pixels so a style
// that fits its words to the circle keeps a handful rather than one per pixel.
func (r *roundRenderer) styleFace(bold bool, size int) font.Face {
	spotStyleFont.once.Do(func() {
		spotStyleFont.bold, _ = opentype.Parse(gobold.TTF)
		spotStyleFont.regular, _ = opentype.Parse(goregular.TTF)
	})
	px := max(size/2*2, 8)
	k := spotFaceKey{bold, px}
	if f, ok := r.styleFaces[k]; ok {
		return f
	}
	src := spotStyleFont.regular
	if bold {
		src = spotStyleFont.bold
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
		r.styleFaces = map[spotFaceKey]font.Face{}
	}
	r.styleFaces[k] = f
	return f
}

// fitted is face at size, made smaller until s fits w.
func (r *roundRenderer) fitted(bold bool, size int, s string, w int) font.Face {
	f := r.styleFace(bold, size)
	if tw := r.width(f, s); tw > w {
		f = r.styleFace(bold, size*w/tw)
	}
	return f
}

// styledClockFace is the clock face in a style other than the classic one.
func (r *roundRenderer) styledClockFace(s roundScene, style string) {
	// The alert pill last, over the face: the larger styles reach up where it sits.
	defer r.alertPill(s.alerts.Here, clockPillY)
	switch style {
	case styleBig:
		r.bigFace(s)
	case styleFlip:
		r.flipFace(s)
	case styleLED:
		r.ledFace(s)
	case styleAnalog:
		r.analogFace(s)
	case styleWords:
		r.wordsFace(s)
	case styleSun:
		r.sunFace(s)
	case styleDashboard:
		r.dashboardFace(s)
	case styleBinary:
		r.binaryFace(s)
	case styleWorld:
		r.worldFace(s)
	case styleAgenda:
		r.agendaFace(s)
	case styleGlow:
		r.glowFace(s)
	}
}

// footLine is the one line a style keeps at its foot: a running timer, else a missed alarm, else
// what the style would say there anyway.
func (r *roundRenderer) footLine(s roundScene, baseline int, otherwise string) {
	switch {
	case len(s.timers) > 0:
		r.centered(r.body, "Timer "+clockDuration(s.timers[0].Left), baseline, colTimer)
	case s.missed != "":
		r.centered(r.small, s.missed, baseline, colTimer)
	case s.slideshowTrouble != "":
		r.centered(r.small, s.slideshowTrouble, baseline, colDim)
	case otherwise != "":
		r.centered(r.small, otherwise, baseline, colDim)
	}
}

// statusWord is the word the classic face puts over the time.
func (r *roundRenderer) statusWord(s roundScene, baseline int) {
	switch {
	case s.setupAsking:
		r.centered(r.label, "A BROWSER IS ASKING", baseline, colMuted)
	case s.muted:
		r.centered(r.label, "MICROPHONE OFF", baseline, colMuted)
	case s.btPairing:
		r.centered(r.label, "BLUETOOTH PAIRING", baseline, colBluetooth)
	case s.playing:
		r.centered(r.label, "PLAYING", baseline, colDim)
	case s.paused:
		r.centered(r.label, "PAUSED", baseline, colDim)
	}
}

// shortDay is the day and the temperature, for a style's foot: "Wed 16  ·  72°".
func shortDay(s roundScene) string {
	line := locale.DayAndNumber(s.now, screenLang())
	if s.weather.Temp != "" {
		line += "  ·  " + s.weather.Temp
	}
	return line
}

// hourMinute is the time's two halves as the clock format writes them.
func hourMinute(now time.Time) (string, string) {
	hm := strings.SplitN(clockHM(now), ":", 2)
	if len(hm) < 2 {
		return clockHM(now), ""
	}
	return hm[0], hm[1]
}

// bigFace is the hours over the minutes, as large as the circle allows.
func (r *roundRenderer) bigFace(s roundScene) {
	h, m := hourMinute(s.now)
	f := r.styleFace(true, 170)
	r.centered(f, h, 212, colText)
	r.centered(f, m, 398, colAccent)
	if ampm := clockSuffix(s.now); ampm != "" {
		r.line(90, 236, 196, 236, 2, colTrack)
		r.line(284, 236, 390, 236, 2, colTrack)
		r.centered(r.label, ampm, 244, colDim)
	} else {
		r.line(120, 236, 360, 236, 2, colTrack)
	}
	r.footLine(s, 444, shortDay(s))
}

// flipFace is the time on flip cards.
func (r *roundRenderer) flipFace(s roundScene) {
	r.statusWord(s, 118)
	h, m := hourMinute(s.now)
	cards := []string{}
	if len(h) == 2 {
		cards = append(cards, h[:1])
	}
	cards = append(cards, h[len(h)-1:], m[:1], m[1:])
	const cw, ch, gap, apart = 86, 150, 8, 22
	total := len(cards)*cw + (len(cards)-2)*gap + apart
	x, y := center-total/2, 140
	f := r.styleFace(true, 118)
	for i, c := range cards {
		b := image.Rect(x, y, x+cw, y+ch)
		r.roundFill(b, 10, color.RGBA{36, 42, 52, 255}, color.RGBA{28, 33, 41, 255})
		r.cardFigure(f, c, image.Rect(x, y+ch*12/100, x+cw, y+ch-ch*12/100))
		r.line(float64(x), float64(y+ch/2), float64(x+cw), float64(y+ch/2), 3, colBackground)
		x += cw + gap
		if i == len(cards)-3 {
			x += apart - gap
		}
	}
	line := shortDay(s)
	if ampm := clockSuffix(s.now); ampm != "" {
		line = ampm + "  ·  " + line
	}
	r.centered(r.small, line, 330, colDim)
	r.footLine(s, 372, "")
}

// cardFigure draws a flip card's figure stretched tall and narrow into box, as the night's flip clock
// does. Zero is the face's round O, since its own zero carries a slash.
func (r *roundRenderer) cardFigure(face font.Face, s string, box image.Rectangle) {
	c := []rune(s)[0]
	if c == '0' {
		c = 'O'
	}
	b, _ := font.BoundString(face, string(c))
	gw, gh := (b.Max.X - b.Min.X).Ceil(), (b.Max.Y - b.Min.Y).Ceil()
	if gw <= 0 || gh <= 0 {
		return
	}
	glyph := image.NewRGBA(image.Rect(0, 0, gw, gh))
	(&font.Drawer{Dst: glyph, Src: image.NewUniform(colText), Face: face,
		Dot: fixed.Point26_6{X: -b.Min.X, Y: -b.Min.Y}}).DrawString(string(c))
	h := box.Dy()
	w := min(gw*h/gh*72/100, box.Dx()*80/100)
	if c == '1' {
		w = min(w, box.Dx()*44/100)
	}
	x := box.Min.X + (box.Dx()-w)/2
	xdraw.BiLinear.Scale(r.dst, image.Rect(x, box.Min.Y, x+w, box.Max.Y), glyph, glyph.Bounds(), xdraw.Over, nil)
}

// capHeightOf is how tall face's figures stand above the baseline.
func capHeightOf(face font.Face) int {
	if b, _, ok := face.GlyphBounds('8'); ok {
		return (-b.Min.Y).Ceil()
	}
	return face.Metrics().Ascent.Ceil() * 7 / 10
}

// segmentsLit are the seven segments each digit lights: a top, b top right, c bottom right, d bottom,
// e bottom left, f top left, g middle.
var segmentsLit = [10]string{"abcdef", "bc", "abdeg", "abcdg", "bcfg", "acdfg", "acdefg", "abc", "abcdefg", "abcdfg"}

// ledFace is the time in seven-segment digits, the unlit segments faintly behind.
func (r *roundRenderer) ledFace(s roundScene) {
	r.statusWord(s, 118)
	h, m := hourMinute(s.now)
	digits := []int{-1, -1, -1, -1}
	if len(h) == 2 {
		digits[0] = int(h[0] - '0')
	}
	digits[1] = int(h[len(h)-1] - '0')
	if len(m) == 2 {
		digits[2], digits[3] = int(m[0]-'0'), int(m[1]-'0')
	}
	const dw, dh, t = 40.0, 84.0, 9.0
	gap, colon := dw*0.42, t*2
	ampm := clockSuffix(s.now)
	aw := 0.0
	if ampm != "" {
		aw = float64(r.width(r.label, ampm)) + 8
	}
	total := 4*dw + 3*gap + colon + aw
	x, top := float64(center)-total/2, 184.0
	lit, ghost := colAccent, lerp(colBackground, colAccent, 0.1)
	for i, d := range digits {
		r.segmentDigitAt(x, top, dw, dh, t, d, lit, ghost)
		x += dw + gap
		if i == 1 {
			cx := x - gap/2 + colon/2
			r.discAt(cx, top+dh*0.3, t*0.55, lit)
			r.discAt(cx, top+dh*0.7, t*0.55, lit)
			x += colon
		}
	}
	if ampm != "" {
		r.text(r.label, ampm, int(x-gap+8), int(top+dh), lit)
	}
	r.centered(r.small, locale.LongDate(s.now, screenLang()), 330, colDim)
	r.footLine(s, 372, "")
}

// segmentDigitAt draws one seven-segment digit, w by h at x, top, each segment t thick.
func (r *roundRenderer) segmentDigitAt(x, top, w, h, t float64, digit int, lit, ghost color.RGBA) {
	on := ""
	if digit >= 0 && digit <= 9 {
		on = segmentsLit[digit]
	}
	g := t * 0.9
	mid := top + h/2
	for _, sg := range []struct {
		name           byte
		x0, y0, x1, y1 float64
	}{
		{'a', x + g, top, x + w - g, top},
		{'d', x + g, top + h, x + w - g, top + h},
		{'g', x + g, mid, x + w - g, mid},
		{'f', x, top + g, x, mid - g},
		{'b', x + w, top + g, x + w, mid - g},
		{'e', x, mid + g, x, top + h - g},
		{'c', x + w, mid + g, x + w, top + h - g},
	} {
		c := ghost
		if strings.IndexByte(on, sg.name) >= 0 {
			c = lit
		}
		r.line(sg.x0, sg.y0, sg.x1, sg.y1, t, c)
	}
}

// analogFace is a dial across the whole face, the weather above the middle and the date below it.
func (r *roundRenderer) analogFace(s roundScene) {
	const R = 212.0
	at := func(dist, deg float64) (float64, float64) {
		a := (deg - 90) * math.Pi / 180
		return center + dist*math.Cos(a), center + dist*math.Sin(a)
	}
	for i := range 60 {
		long := i%5 == 0
		in, w, c := R-12, 2.0, color.RGBA{70, 80, 92, 255}
		if long {
			in, w, c = R-26, 4, colText
		}
		x0, y0 := at(R, float64(i*6))
		x1, y1 := at(in, float64(i*6))
		r.line(x0, y0, x1, y1, w, c)
	}
	num := r.styleFace(false, 28)
	for n, deg := range map[string]float64{"12": 0, "3": 90, "6": 180, "9": 270} {
		x, y := at(R-52, deg)
		r.centered2(num, n, int(x), int(y)+capHeightOf(num)/2, colDim)
	}
	if w := s.weather; w.Temp != "" {
		const u = 11.0
		tw := r.width(r.small, w.Temp)
		left := center - (tw+int(2*u)+8)/2
		if w.Condition != "" {
			r.weatherIcon(w.Condition, float64(left)+u, 142, u)
		}
		r.text(r.small, w.Temp, left+int(2*u)+8, 150, colText)
	}
	day := strings.ToUpper(locale.DayAndNumber(s.now, screenLang()))
	dw := r.width(r.label, day) + 20
	r.roundFill(image.Rect(center-dw/2, 302, center+dw/2, 334), 8, color.RGBA{24, 29, 36, 255}, color.RGBA{24, 29, 36, 255})
	r.centered(r.label, day, 325, colText)
	r.footLine(s, 362, "")
	hand := func(deg, length, back, w float64, c color.RGBA) {
		x0, y0 := at(-back, deg)
		x1, y1 := at(length, deg)
		r.line(x0, y0, x1, y1, w, c)
	}
	h, m, sec := float64(s.now.Hour()%12), float64(s.now.Minute()), float64(s.now.Second())
	hand((h+m/60)*30, 104, 16, 12, colText)
	hand((m+sec/60)*6, 166, 16, 8, colText)
	hand(sec*6, 178, 28, 3, colAccent)
	r.discAt(center, center, 9, colAccent)
	r.discAt(center, center, 4, colBackground)
	r.over.note(image.Rect(center-int(R), center-int(R), center+int(R), center+int(R)))
}

// wordsFace says the time down the middle of the circle.
func (r *roundRenderer) wordsFace(s roundScene) {
	lead, hour, period := clockWords(s.now)
	number, link := lead, ""
	for _, w := range []string{" past", " to"} {
		if strings.HasSuffix(lead, w) {
			number, link = strings.TrimSuffix(lead, w), strings.TrimSpace(w)
		}
	}
	room := func(y int) int { return chord(y) - 60 }
	if lead == "" {
		r.centered(r.styleFace(false, 30), "it's", 150, colDim)
		r.centered(r.fitted(true, 84, hour, room(236)), hour, 236, colAccent)
		if period != "" {
			r.centered(r.styleFace(false, 22), period, 290, colDim)
		}
		r.footLine(s, 360, shortDay(s))
		return
	}
	r.centered(r.styleFace(false, 28), "it's", 110, colDim)
	r.centered(r.fitted(true, 64, number, room(168)), number, 168, colText)
	r.centered(r.styleFace(false, 30), link, 214, colDim)
	r.centered(r.fitted(true, 84, hour, room(298)), hour, 298, colAccent)
	if period != "" {
		r.centered(r.styleFace(false, 22), period, 344, colDim)
	}
	r.footLine(s, 394, shortDay(s))
}

// sunFace is the time over the day's light on the rim (sunRim); until home's place is known, the
// classic face.
func (r *roundRenderer) sunFace(s roundScene) {
	if !s.style.sunOK {
		r.classicClockFace(s)
		return
	}
	r.statusWord(s, 118)
	r.timeLine(s.now, 206)
	r.centered(r.small, locale.LongDate(s.now, screenLang()), 256, colDim)
	line := 298
	if weatherLine(s.weather) != "" {
		r.clockWeather(s.weather, line)
		line += 38
	}
	if len(s.timers) > 0 || s.missed != "" {
		r.footLine(s, line, "")
		line += 34
	}
	warm := color.RGBA{230, 170, 120, 255}
	r.centered(r.tiny, "Sun up "+clockText(s.style.rise)+"  ·  down "+clockText(s.style.set), line+4, warm)
}

// sunRim is the rim as the whole day: noon at the top, midnight at the bottom, daylight from sunrise
// to sunset in warm colors, and a dot where now is.
func (r *roundRenderer) sunRim(s roundScene) {
	angle := func(t time.Time) float64 {
		h := float64(t.Hour()) + float64(t.Minute())/60
		return math.Mod((h-12)/24*2*math.Pi+4*math.Pi, 2*math.Pi)
	}
	a0, a1 := angle(s.style.rise), angle(s.style.set)
	if a1 < a0 {
		a1 += 2 * math.Pi
	}
	r.sunRing(a0, a1)
	mid := (rimIn + rimOut) / 2.0
	for _, t := range []time.Time{s.style.rise, s.style.set} {
		a := angle(t)
		r.discAt(center+mid*math.Sin(a), center-mid*math.Cos(a), 3, color.RGBA{255, 214, 150, 255})
	}
	a := angle(s.now)
	x, y := center+mid*math.Sin(a), center-mid*math.Cos(a)
	r.discAt(x, y, 12, colBackground)
	r.discAt(x, y, 9, colText)
	for _, h := range []struct {
		label string
		deg   float64
	}{{"12", 0}, {"6", 90}, {"12", 180}, {"6", 270}} {
		a := h.deg * math.Pi / 180
		d := float64(rimIn) - 20
		r.centered2(r.tiny, h.label, int(center+d*math.Sin(a)), int(center-d*math.Cos(a))+6, colDim)
	}
}

// sunRing is the rim in one pass: night blue all round, and daylight from a0 to a1 (radians from the
// top, clockwise; a1 may run past a full turn), warm in the middle of the day and rosier at its ends.
// One pass over the ring rather than an arc per shade, each of which is a pass of its own.
func (r *roundRenderer) sunRing(a0, a1 float64) {
	night := color.RGBA{22, 30, 52, 255}
	noon, edge := color.RGBA{255, 196, 70, 255}, color.RGBA{240, 110, 80, 255}
	cx, cy := float64(center), float64(center)
	r0, r1 := float64(rimIn), float64(rimOut)
	outer, inner := r1+1, r0-1
	b := r.dst.Rect
	span := a1 - a0
	for y := max(int(cy-r1)-1, b.Min.Y); y <= min(int(cy+r1)+1, b.Max.Y-1); y++ {
		dy := float64(y) + 0.5 - cy
		if math.Abs(dy) > outer {
			continue
		}
		xo := math.Sqrt(outer*outer-dy*dy) + 1
		spans := [][2]int{{int(cx - xo), int(cx + xo)}}
		if math.Abs(dy) < inner-1 {
			xi := math.Sqrt(inner*inner-dy*dy) - 1
			spans = [][2]int{{int(cx - xo), int(cx - xi)}, {int(cx+xi) + 1, int(cx + xo)}}
		}
		for _, sp := range spans {
			for x := max(sp[0], b.Min.X); x <= min(sp[1], b.Max.X-1); x++ {
				dx := float64(x) + 0.5 - cx
				d := math.Hypot(dx, dy)
				if d < r0-1 || d > r1+1 {
					continue
				}
				c := night
				rel := math.Mod(math.Atan2(dx, -dy)-a0+4*math.Pi, 2*math.Pi)
				if rel <= span {
					e := math.Abs(rel/span-0.5) * 2
					c = lerp(noon, edge, e*e)
				}
				r.blend(x, y, c, math.Min(math.Min(d-(r0-1), (r1+1)-d), 1))
			}
		}
	}
}

// dashboardFace is the time, the next two events and three days of weather.
func (r *roundRenderer) dashboardFace(s roundScene) {
	h := r.styleFace(true, 76)
	hm := clockHM(s.now)
	ampm := clockSuffix(s.now)
	aw := 0
	if ampm != "" {
		aw = r.width(r.label, ampm) + 6
	}
	x := center - (r.width(h, hm)+aw)/2
	r.text(h, hm, x, 138, colText)
	if ampm != "" {
		r.text(r.label, ampm, x+r.width(h, hm)+6, 138, colAccent)
	}
	r.centered(r.styleFace(false, 20), locale.LongDate(s.now, screenLang()), 172, colDim)
	r.line(90, 196, 390, 196, 2, colTrack)

	f := r.styleFace(false, 20)
	if len(s.style.next) == 0 {
		r.centered(f, "Nothing coming up", 246, colDim)
	}
	for i, e := range s.style.next[:min(len(s.style.next), 2)] {
		y := 232 + i*36
		when := dashWhenSpot(e.Start, e.AllDay, s.now)
		r.text(f, when, 96, y, colTimer)
		tx := 96 + 100
		r.text(f, spotClip(r, f, e.Summary, 392-tx), tx, y, colText)
	}
	r.line(90, 290, 390, 290, 2, colTrack)
	if len(s.timers) > 0 || s.missed != "" {
		r.footLine(s, 336, "")
		return
	}
	days := s.style.days[:min(len(s.style.days), 3)]
	for i, d := range days {
		cx := center - 100 + i*100
		name := locale.ShortWeekday(d.When, screenLang())
		if i == 0 {
			name = "Now"
		}
		r.centered2(r.styleFace(true, 18), name, cx, 318, colDim)
		r.weatherIcon(d.Condition, float64(cx), 350, 13)
		r.centered2(r.styleFace(false, 22), fmt.Sprintf("%.0f°", d.High), cx, 392, colText)
	}
}

// dashWhenSpot is when an event starts, short enough for the circle.
func dashWhenSpot(start time.Time, allDay bool, now time.Time) string {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	tomorrow := today.AddDate(0, 0, 1)
	switch {
	case !allDay && !start.After(now):
		return "Now"
	case allDay && start.Before(tomorrow):
		return "Today"
	case start.Before(tomorrow):
		return clockText(start)
	case start.Before(tomorrow.AddDate(0, 0, 1)):
		return "Tmrw"
	}
	return locale.ShortWeekday(start, screenLang())
}

// spotClip is s cut to fit w, with an ellipsis.
func spotClip(r *roundRenderer, f font.Face, s string, w int) string {
	if r.width(f, s) <= w {
		return s
	}
	rs := []rune(s)
	for len(rs) > 0 && r.width(f, string(rs)+"…") > w {
		rs = rs[:len(rs)-1]
	}
	return string(rs) + "…"
}
