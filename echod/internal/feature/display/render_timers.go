//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"time"

	"golang.org/x/image/vector"

	"github.com/HuskerMinion/techo5/echod/internal/feature/timer"
)

// The timer styles on the clock (timer_style.go). Line, Ring and Banner take the foot of the clock,
// where the line always went, so every clock style makes the same room for them; Corner and Big take
// none, and the clock is laid out as if no timer ran.

// maxTimerCards is how many timers Ring and Banner draw side by side; the rest are "+n".
const maxTimerCards = 3

// runningTimers are the timers counting down, soonest first. Paused ones are not on the clock.
func runningTimers(s scene) []timer.Countdown {
	var out []timer.Countdown
	for _, t := range s.timers {
		if t.Active {
			out = append(out, t)
		}
	}
	return out
}

// timersAtFoot is whether running timers take the foot of the clock in the style in force.
func timersAtFoot(s scene) bool {
	if len(runningTimers(s)) == 0 {
		return false
	}
	switch timerStyle() {
	case timerCorner, timerBig:
		return false
	}
	return true
}

// timersFoot draws the running timers at the foot, the line's baseline at y.
func (r *renderer) timersFoot(s scene, y int) {
	switch timerStyle() {
	case timerRing:
		r.timerRings(s, y)
	case timerBanner:
		r.timerBanner(s, y)
	default:
		r.timersLine(s, y)
	}
}

// timerName is what a timer is called on the clock.
func timerName(t timer.Countdown) string {
	if t.Name != "" {
		return t.Name
	}
	return "Timer"
}

// timerFrac is how much of t is left, 0 to 1; a timer without a length reads as full.
func timerFrac(t timer.Countdown) float64 {
	if t.Total <= 0 {
		return 1
	}
	return math.Min(math.Max(float64(t.Left)/float64(t.Total), 0), 1)
}

// timerArc is a ring at cx, cy, w thick: the whole of it faint in the rules color, and the part left
// in the accent, from the top round clockwise, emptying as the time runs out.
func (r *renderer) timerArc(cx, cy, rad, w, left float64) {
	r.arcBand(cx, cy, rad, w, 0, 360, ember)
	if left > 0 {
		r.arcBand(cx, cy, rad, w, 0, 360*left, amber)
	}
}

// arcBand fills the band of a ring rad round its middle, w thick, from degree from to degree to,
// clockwise from the top.
func (r *renderer) arcBand(cx, cy, rad, w, from, to float64, c color.RGBA) {
	out, in := rad+w/2, rad-w/2
	box := image.Rect(int(cx-out)-1, int(cy-out)-1, int(cx+out)+2, int(cy+out)+2)
	ox, oy := cx-float64(box.Min.X), cy-float64(box.Min.Y)
	steps := max(int((to-from)/4), 2)
	at := func(rr, deg float64) (float32, float32) {
		a := (deg - 90) * math.Pi / 180
		return float32(ox + rr*math.Cos(a)), float32(oy + rr*math.Sin(a))
	}
	r.fillShape(box, c, func(z *vector.Rasterizer, _, _ float32) {
		z.MoveTo(at(out, from))
		for i := 1; i <= steps; i++ {
			z.LineTo(at(out, from+(to-from)*float64(i)/float64(steps)))
		}
		for i := steps; i >= 0; i-- {
			z.LineTo(at(in, from+(to-from)*float64(i)/float64(steps)))
		}
	})
}

// timerRings is a ring for each running timer with its name and time beside it, on one line,
// centered at the foot.
func (r *renderer) timerRings(s scene, y int) {
	run := runningTimers(s)
	if len(run) == 0 {
		return
	}
	shown, more := run, 0
	if len(run) > maxTimerCards {
		shown, more = run[:maxTimerCards], len(run)-maxTimerCards
	}
	ch := capHeight(r.body)
	d := ch * 3 / 2
	gap, word, between := r.s(12), r.s(10), r.s(44)
	type card struct {
		name, left string
		w          int
	}
	extra := ""
	if more > 0 {
		extra = fmt.Sprintf("+%d", more)
	}
	cards := make([]card, len(shown))
	total := 0
	lay := func(nameW int) {
		total = 0
		for i, t := range shown {
			c := card{name: r.clipTo(r.small, timerName(t), nameW), left: timer.LeftText(t.Left)}
			c.w = d + gap + r.width(r.small, c.name) + word + r.width(r.body, c.left)
			cards[i] = c
			total += c.w
		}
		total += between * (len(cards) - 1)
		if extra != "" {
			total += between + r.width(r.body, extra)
		}
	}
	lay(r.s(200))
	if room := r.w - 2*r.margin; total > room {
		// Three long names do not fit a Show 5: each gets an even share of what the rings and times
		// leave.
		names := 0
		for _, c := range cards {
			names += r.width(r.small, c.name)
		}
		lay(max((room-(total-names))/len(cards), 0))
	}
	x := (r.w - total) / 2
	for i, c := range cards {
		r.timerArc(float64(x)+float64(d)/2, float64(y-ch/2), float64(d)/2-float64(r.s(3)), float64(max(r.s(5), 3)), timerFrac(shown[i]))
		tx := x + d + gap
		r.text(r.small, c.name, tx, y, dim)
		tx += r.width(r.small, c.name) + word
		r.text(r.body, c.left, tx, y, amber)
		x += c.w + between
	}
	if extra != "" {
		r.text(r.body, extra, x, y, dim)
	}
}

// timerBanner is a card for each running timer across the foot: its name, its time, and a bar of what
// is left along the bottom.
func (r *renderer) timerBanner(s scene, y int) {
	run := runningTimers(s)
	if len(run) == 0 {
		return
	}
	shown, more := run, 0
	if len(run) > maxTimerCards {
		shown, more = run[:maxTimerCards], len(run)-maxTimerCards
	}
	gap := r.s(14)
	top, bot := y-r.s(40), y+r.s(16)
	right := r.w - r.margin
	extra := ""
	if more > 0 {
		extra = fmt.Sprintf("+%d", more)
		right -= r.width(r.body, extra) + gap
	}
	cw := (right - r.margin - gap*(len(shown)-1)) / len(shown)
	cw = min(cw, r.s(420))
	x := (r.margin + right - (cw*len(shown) + gap*(len(shown)-1))) / 2
	pad := r.s(16)
	for _, t := range shown {
		b := image.Rect(x, top, x+cw, bot)
		r.roundRect(b, r.s(12), surface(4))
		left := timer.LeftText(t.Left)
		lw := r.width(r.body, left)
		base := b.Max.Y - r.s(18)
		r.text(r.body, left, b.Max.X-pad-lw, base, amber)
		r.text(r.small, r.clipTo(r.small, timerName(t), b.Dx()-3*pad-lw), b.Min.X+pad, base-r.s(2), cream)
		bar := image.Rect(b.Min.X+pad, b.Max.Y-r.s(9), b.Max.X-pad, b.Max.Y-r.s(5))
		draw.Draw(r.dst, bar, image.NewUniform(ember), image.Point{}, draw.Src)
		bar.Max.X = bar.Min.X + int(float64(bar.Dx())*timerFrac(t))
		draw.Draw(r.dst, bar, image.NewUniform(amber), image.Point{}, draw.Src)
		x += cw + gap
	}
	if extra != "" {
		r.text(r.body, extra, r.w-r.margin-r.width(r.body, extra), bot-r.s(18), dim)
	}
}

// timerCorner is a small badge at the top right: a ring for the soonest timer, its time, and how many
// more. Under the alert badge when there is one, under the weather when that is at the top right, and
// under the HAL 9000 skin's eye.
func (r *renderer) timerCorner(s scene, on bool) {
	run := runningTimers(s)
	if !on || len(run) == 0 {
		return
	}
	first := run[0]
	label := timer.LeftText(first.Left)
	if n := len(run) - 1; n > 0 {
		label += fmt.Sprintf("  +%d", n)
	}
	h := r.s(40)
	top := r.badgeTop()
	if len(s.alerts.Here) > 0 {
		top += h + r.s(10)
	}
	if r.weatherRight {
		// The second row, where it goes under an alert, and under the badge if that went under the weather.
		top = max(top, r.badgeTop()+h+r.s(10))
		if b := r.alertBadgeAt(); !b.Empty() {
			top = max(top, b.Max.Y+r.s(10))
		}
	}
	if eye := r.halEyeAt(s); !eye.Empty() {
		top = max(top, eye.Max.Y+r.s(10))
	}
	d := h - r.s(12)
	w := r.s(14) + d + r.s(10) + r.width(r.body, label) + r.s(18)
	b := image.Rect(r.w-r.margin-w, top, r.w-r.margin, top+h)
	r.roundRect(b, h/2, surface(5))
	cx := float64(b.Min.X + r.s(14) + d/2)
	r.timerArc(cx, float64(b.Min.Y+h/2), float64(d)/2-1, float64(r.s(4)), timerFrac(first))
	r.text(r.body, label, b.Min.X+r.s(14)+d+r.s(10), b.Max.Y-r.s(9), amber)
}

// bigTimer is the soonest timer in place of the clock: its name, its time as large as the screen
// takes, a bar of what is left, and under it the time of day and the timers after it.
func (r *renderer) bigTimer(s scene) {
	run := runningTimers(s)
	first := run[0]
	box := image.Rect(r.margin, r.s(84), r.w-r.margin, r.h-r.s(60))
	left := timer.LeftText(first.Left)
	// The largest face whose figures fit the width and half the height.
	size := 300
	f := r.styleFace(true, size)
	for size > 60 && (r.width(f, left) > box.Dx()*9/10 || capHeight(f) > box.Dy()/2) {
		size -= 12
		f = r.styleFace(true, size)
	}
	ch := capHeight(f)
	base := box.Min.Y + box.Dy()/2 + ch/2
	name := r.clipTo(r.title, timerName(first), box.Dx())
	r.text(r.title, name, (r.w-r.width(r.title, name))/2, base-ch-r.s(34), dim)
	r.text(f, left, (r.w-r.width(f, left))/2, base, amber)

	bw := min(box.Dx()*7/10, r.width(f, left)+r.s(40))
	bar := image.Rect((r.w-bw)/2, base+r.s(28), (r.w+bw)/2, base+r.s(36))
	r.roundRect(bar, bar.Dy()/2, ember)
	if k := timerFrac(first); k > 0 {
		lit := bar
		lit.Max.X = lit.Min.X + max(int(float64(bar.Dx())*k), bar.Dy())
		r.roundRect(lit, bar.Dy()/2, amber)
	}

	under := clockText(s.now)
	if n := len(run) - 1; n == 1 {
		under += "  ·  " + timerName(run[1]) + " " + timer.LeftText(run[1].Left)
	} else if n > 1 {
		under += fmt.Sprintf("  ·  %d more timers", n)
	}
	r.text(r.small, under, (r.w-r.width(r.small, under))/2, bar.Max.Y+r.s(46), dim)
}

// dialWedge is the soonest timer on the Analog style's dial, in the Ring style: a wedge from the
// minute hand round to where it will be when the timer ends. An hour or more is the whole dial,
// fainter.
func (r *renderer) dialWedge(s scene, cx, cy, rad float64) {
	if timerStyle() != timerRing {
		return
	}
	run := runningTimers(s)
	if len(run) == 0 {
		return
	}
	left := run[0].Left
	mins := float64(s.now.Minute()) + float64(s.now.Second())/60
	from := mins * 6
	sweep := math.Min(left.Minutes()*6, 360)
	alpha := uint8(84)
	if left >= time.Hour {
		alpha = 40
	}
	rr := rad * 0.9
	box := image.Rect(int(cx-rad), int(cy-rad), int(cx+rad)+1, int(cy+rad)+1)
	ox, oy := cx-float64(box.Min.X), cy-float64(box.Min.Y)
	r.fillShape(box, faded(amber, alpha), func(z *vector.Rasterizer, w, h float32) {
		z.MoveTo(float32(ox), float32(oy))
		steps := max(int(sweep/3), 2)
		for i := 0; i <= steps; i++ {
			a := (from + sweep*float64(i)/float64(steps) - 90) * math.Pi / 180
			z.LineTo(float32(ox+rr*math.Cos(a)), float32(oy+rr*math.Sin(a)))
		}
	})
}
