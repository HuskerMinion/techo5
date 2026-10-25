// Package rainbow draws the Rainbow turn screen (display's turnStyles) over whatever page is up: a light
// running round the edge of the screen in saturated pinks, violets, blues and oranges, stretched
// along the top and the bottom, moving with the voice being heard and the answer being spoken, and a
// card of frosted glass in the middle with the words. A turn opens with a gold globe swelling at the
// right edge and a wave of pink crossing the screen, warping and frosting the page as it goes, the
// whole page drawing back a little and then settling.
//
// Every size here is in design units, a screen 640 across: a screen is k of its pixels to one. It is
// all drawn on the processor in a frame of 50 ms, so nothing is worked out per pixel that need not be:
// where each pixel sits along the edge is worked out once per screen size; the glow at a quarter of
// the screen's size and its sharp line at half, doubled up onto the screen; the frost from blurred
// copies made once when a turn begins; and the card's glass from a blur at a quarter of its size.
package rainbow

import (
	"image"
	"math"
	"sync"
	"time"

	"golang.org/x/image/font"
)

// Turn is what the screen is doing: the turn's phase (listening, thinking, replying, lingering with
// the answer after it, or "" between turns), how loud the voice is, 0 to 1, and the words.
type Turn struct {
	Phase        string
	Level        float64
	Heard, Reply string
}

// Faces are the card's: what was heard, and the answer.
type Faces struct {
	Heard, Reply font.Face
}

// lit is whether the light is on in phase.
func lit(phase string) bool {
	return phase == "listening" || phase == "thinking" || phase == "replying"
}

// Screen is the drawing kept between frames: where the edge is, how far into a turn it is, the
// light's strength and the card's.
type Screen struct {
	mu   sync.Mutex
	geo  *geometry
	want [2]int // the size being prepared, while geo is someone else's

	last     time.Time // the frame before
	step     float64   // the time since it, in seconds
	vis      float64   // how far the light is on, 0 to 1
	lvl      float64   // the voice, smoothed
	boost    float64   // the entry's push on the light, along the right
	began    time.Time // when the light came on
	wasLit   bool
	src      *entrySrc
	card     card
	glowBufs glowBufs
	frame    frame // Begin's, for Finish
}

// Workers is how many goroutines the heavy parts are split over: the Show's four cores but one, left
// to the audio. Not worked out from GOMAXPROCS: at start-up that is however many cores the kernel's
// hotplug had awake, often two, and TECHO5 sizes the runtime to the cores present (android/setup)
// only after this package is set up.
var Workers = 3

// Prepare starts working out the edge for a screen w by h in the background, so the first turn does
// not wait on it. Cheap once it is done or under way.
func (s *Screen) Prepare(w, h int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if (s.geo != nil && s.geo.w == w && s.geo.h == h) || s.want == [2]int{w, h} {
		return
	}
	s.want = [2]int{w, h}
	go func() {
		g := newGeometry(w, h)
		s.mu.Lock()
		if s.want == [2]int{w, h} && (s.geo == nil || s.geo.w != w || s.geo.h != h) {
			s.geo = g
		}
		s.mu.Unlock()
	}()
}

// geometryFor is the edge for dst, worked out now if Prepare has not been asked or is not done.
func (s *Screen) geometryFor(w, h int) *geometry {
	s.mu.Lock()
	g := s.geo
	s.mu.Unlock()
	if g != nil && g.w == w && g.h == h {
		return g
	}
	g = newGeometry(w, h)
	s.mu.Lock()
	s.geo, s.want = g, [2]int{w, h}
	s.mu.Unlock()
	return g
}

// The entry's length, in seconds, and how long into it the card may come.
const (
	entryEnd = 1.4
	cardFrom = 1.0
)

// Draw draws the turn over the page already on dst at now, and says where the card is, for a tap on
// it; empty when there is none. Between turns it costs nothing once the light has gone out.
func (s *Screen) Draw(dst *image.RGBA, now time.Time, t Turn, f Faces) image.Rectangle {
	s.Begin(dst.Rect.Dx(), dst.Rect.Dy(), now, t)
	return s.Finish(dst, f)
}

// Begin is Draw's first half, before the page is drawn on a screen w by h: the turn moves on to now,
// so the caller can ask whether there is anything to draw (Active, Busy) before drawing the page.
// Finish is the rest, over the page. A Begin without its Finish is let go by the next Begin.
func (s *Screen) Begin(w, h int, now time.Time, t Turn) {
	dt := 0.0
	if !s.last.IsZero() {
		dt = min(max(now.Sub(s.last).Seconds(), 0), 0.1)
	}
	s.last, s.step = now, dt
	on := lit(t.Phase)
	if !on && s.vis < 0.004 && !s.card.up() {
		s.vis, s.wasLit, s.src, s.boost = 0, false, nil, 0
		s.frame = frame{}
		return
	}
	g := s.geometryFor(w, h)
	capture := false
	if on && !s.wasLit && s.vis < 0.3 {
		// A turn beginning: the entry, from the page as it is now, once it is drawn.
		s.began, s.vis = now, 0
		capture = true
	}
	s.wasLit = on
	a := now.Sub(s.began).Seconds()

	target := 0.0
	if t.Phase == "listening" || t.Phase == "replying" {
		target = t.Level
	}
	// Rates per sixtieth of a second, the same in time at any pace.
	follow := 0.15 // falling slowly
	if target > s.lvl {
		follow = 0.5
	}
	s.lvl += (target - s.lvl) * rate(follow, dt)
	if on {
		up := 0.0
		if a > 0.16 {
			up = 1
		}
		s.vis += (up - s.vis) * rate(0.16, dt)
	} else {
		s.vis += (0 - s.vis) * rate(0.08, dt)
	}

	s.boost = 0
	entering := on && a < entryEnd && (s.src != nil || capture)
	if entering {
		s.boost = push(a)
	} else if !on || a >= entryEnd {
		s.src = nil
	}
	glowing := s.vis > 0.004
	if glowing {
		s.glowBegin(g, now, t.Phase == "thinking" && s.boost < 0.05)
	}
	s.frame = frame{g: g, now: now, t: t, dt: dt, a: a, on: on, capture: capture, entering: entering, glowing: glowing}
}

// frame is what Begin worked out for Finish.
type frame struct {
	g                              *geometry
	now                            time.Time
	t                              Turn
	dt, a                          float64
	on, capture, entering, glowing bool
}

// Finish draws the turn Begin moved on over the page now on dst, and says where the card is.
func (s *Screen) Finish(dst *image.RGBA, f Faces) image.Rectangle {
	fr := s.frame
	s.frame = frame{g: fr.g}
	if fr.g == nil {
		return image.Rectangle{}
	}
	g := fr.g
	if fr.capture {
		s.src = captureEntry(dst, s.src)
	}
	// The card's glass is the page under it, blurred, as it is before anything is drawn over it.
	cr := s.card.update(g, fr.now, fr.dt, fr.t, fr.on && s.vis > 0.6 && fr.a >= cardFrom, f)
	if !cr.Empty() {
		s.card.frost(dst, g, cr)
	}
	if fr.entering {
		s.entry(dst, g, fr.a)
	}
	if fr.glowing {
		s.glowEnd(dst, g)
	}
	// The corners rounded as the light is on, and wholly while the entry is up, before the light has
	// come: the entry is drawn at a quarter of the size, the corners here at the whole. Wholly too
	// until the light is all but gone, which is drawn at half the size: a corner half rounded over it
	// shows its steps.
	on := min(1, 5*s.vis)
	if fr.entering {
		on = max(on, smooth(0, 0.12, fr.a)*(1-smooth(1.1, entryEnd, fr.a)))
	}
	corners(dst, g, on)
	if !cr.Empty() {
		s.card.draw(dst, g, fr.now)
	}
	return s.card.at
}

// Active is whether the frame Begin moved on has anything to draw: a turn, or one going away.
func (s *Screen) Active() bool { return s.frame.g != nil }

// Busy is whether the drawing is moving: a turn's entry, its light, or after it the light going out
// and the card going away or finding its height. The page wants frames at the turn's pace until it is
// not.
func (s *Screen) Busy() bool {
	return s.vis > 0.004 || s.src != nil || s.card.moving()
}

// minf, maxf and clampf are min, max and a clamp to 0..1 for the light's float32s, written out: Go's
// own, careful of NaNs, are a call each on the Show's processor, where the light makes millions a
// second of them.
func minf(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func maxf(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func clampf(x float32) float32 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

// rate is how much of the way a value moving f of the way a sixtieth of a second goes in dt.
func rate(f, dt float64) float64 { return 1 - math.Pow(1-f, dt*60) }

// Easing curves.
func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

func smooth(e0, e1, x float64) float64 {
	t := clamp01((x - e0) / (e1 - e0))
	return t * t * (3 - 2*t)
}

func backOut(p float64) float64 {
	const c1 = 1.5
	return 1 + (c1+1)*math.Pow(p-1, 3) + c1*math.Pow(p-1, 2)
}

// par runs fn over [0, n) in about equal parts on Workers goroutines, this one among them.
func par(n int, fn func(lo, hi int)) { parOn(Workers, n, fn) }

// parOn is par on k goroutines.
func parOn(k, n int, fn func(lo, hi int)) {
	k = min(k, n)
	if k <= 1 {
		fn(0, n)
		return
	}
	var wg sync.WaitGroup
	for i := 1; i < k; i++ {
		wg.Add(1)
		go func(lo, hi int) {
			defer wg.Done()
			fn(lo, hi)
		}(n*i/k, n*(i+1)/k)
	}
	fn(0, n/k)
	wg.Wait()
}
