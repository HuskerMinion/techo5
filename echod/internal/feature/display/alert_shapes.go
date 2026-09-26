//go:build !dot

package display

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// Alert shapes on the rain map, for both screens: a warning's own polygon, or the counties and zones an
// advisory covers. Those in force at home are tinted and outlined bold in the alert's color; the rest
// nearby are a faint line, so the map shows what is coming without shouting about it.
//
// The shapes change only when the alerts do, or the map moves; the rain under them changes every
// second of the loop. So they are drawn once into an overlay of their own and laid over each frame,
// rather than projected and filled again for every picture.

// alertOverlay is the shapes drawn last time, and what they were drawn for.
type alertOverlay struct {
	key string
	img *image.RGBA
}

// alertShapes lays radar's nearby alerts over dst, whose picture is the rain map moved by off and
// scaled by scale (1 on the Show; the Spot crops and may scale). inside, when not nil, is the part of
// dst that shows the map (the Spot's circle): nothing is drawn outside it. cache keeps the overlay.
func alertShapes(dst *image.RGBA, radar home.RadarView, alerts []home.Alert, off image.Point, scale float64,
	inside func(x, y int) bool, cache *alertOverlay) {
	if len(alerts) == 0 {
		return
	}
	var key strings.Builder
	fmt.Fprintf(&key, "%v %v %v %.4f", dst.Rect, radar.Origin, off, scale)
	for _, a := range alerts {
		fmt.Fprintf(&key, "|%s %v %d", a.ID, a.Here, len(a.Rings))
	}
	if cache.img == nil || cache.key != key.String() {
		cache.key, cache.img = key.String(), drawAlertShapes(dst.Rect, radar, alerts, off, scale, inside)
	}
	draw.Draw(dst, dst.Rect, cache.img, dst.Rect.Min, draw.Over)
}

// drawAlertShapes draws the shapes onto a clear picture of size r.
func drawAlertShapes(r image.Rectangle, radar home.RadarView, alerts []home.Alert, off image.Point, scale float64,
	inside func(x, y int) bool) *image.RGBA {
	img := image.NewRGBA(r)
	pen := pen{img, inside, true}
	// Each alert projected once, for the three passes.
	rings := make([][][]image.Point, len(alerts))
	for i, a := range alerts {
		for _, ring := range a.Rings {
			pts := make([]image.Point, 0, len(ring))
			for _, p := range ring {
				q := radar.Pixel(p[0], p[1])
				pts = append(pts, image.Pt(int(float64(q.X-off.X)*scale), int(float64(q.Y-off.Y)*scale)))
			}
			rings[i] = append(rings[i], pts)
		}
	}
	// The faint ones first, the tints next, the bold outlines last: home's on top.
	for pass := 0; pass < 3; pass++ {
		for i := len(alerts) - 1; i >= 0; i-- {
			a := alerts[i]
			switch {
			case pass == 0 && !a.Here:
				for _, r := range rings[i] {
					pen.stroke(r, withAlpha(a.Color, 110), 1)
				}
			case pass == 1 && a.Here && !a.Storm:
				pen.fill(rings[i], a.Color, 0.16)
			case pass == 2 && a.Here:
				w := 2.0
				if a.Storm {
					w = 3
				}
				for _, r := range rings[i] {
					pen.stroke(r, color.RGBA{0, 0, 0, 170}, w+2)
					pen.stroke(r, a.Color, w)
				}
			}
		}
	}
	return img
}

func withAlpha(c color.RGBA, a uint8) color.RGBA { c.A = a; return c }

// pen draws onto dst, only where inside allows (everywhere when it is nil). On a clear overlay (clear)
// it lays color over what is there, alpha and all; on a finished picture it mixes into the color.
type pen struct {
	dst    *image.RGBA
	inside func(x, y int) bool
	clear  bool
}

// strokeRing draws with a pen that may go anywhere on a finished picture.
func strokeRing(dst *image.RGBA, pts []image.Point, c color.RGBA, w float64) {
	pen{dst, nil, false}.stroke(pts, c, w)
}

// blend lays c over the pixel at x, y, at c's alpha times k.
func (p pen) blend(x, y int, c color.RGBA, k float64) {
	dst := p.dst
	if !(image.Point{x, y}.In(dst.Rect)) || p.inside != nil && !p.inside(x, y) {
		return
	}
	a := float64(c.A) / 255 * k
	if a <= 0 {
		return
	}
	i := dst.PixOffset(x, y)
	px := dst.Pix[i : i+4 : i+4]
	if p.clear { // premultiplied: color*a over what is there
		px[0] = uint8(float64(c.R)*a + float64(px[0])*(1-a))
		px[1] = uint8(float64(c.G)*a + float64(px[1])*(1-a))
		px[2] = uint8(float64(c.B)*a + float64(px[2])*(1-a))
		px[3] = uint8(255*a + float64(px[3])*(1-a))
		return
	}
	px[0] = uint8(float64(px[0])*(1-a) + float64(c.R)*a)
	px[1] = uint8(float64(px[1])*(1-a) + float64(c.G)*a)
	px[2] = uint8(float64(px[2])*(1-a) + float64(c.B)*a)
}

// stroke draws a closed line through pts, w pixels wide, with soft edges.
func (p pen) stroke(pts []image.Point, c color.RGBA, w float64) {
	dst := p.dst
	if len(pts) < 2 {
		return
	}
	r := w / 2
	for i := range pts {
		a, b := pts[i], pts[(i+1)%len(pts)]
		if !segmentNear(a, b, dst.Rect, int(r)+2) {
			continue
		}
		minX, maxX := min(a.X, b.X)-int(r)-1, max(a.X, b.X)+int(r)+1
		minY, maxY := min(a.Y, b.Y)-int(r)-1, max(a.Y, b.Y)+int(r)+1
		minX, maxX = max(minX, dst.Rect.Min.X), min(maxX, dst.Rect.Max.X-1)
		minY, maxY = max(minY, dst.Rect.Min.Y), min(maxY, dst.Rect.Max.Y-1)
		for y := minY; y <= maxY; y++ {
			for x := minX; x <= maxX; x++ {
				d := distToSegment(float64(x), float64(y), a, b)
				if k := r + 0.5 - d; k > 0 {
					p.blend(x, y, c, math.Min(k, 1))
				}
			}
		}
	}
}

func segmentNear(a, b image.Point, r image.Rectangle, pad int) bool {
	box := image.Rect(min(a.X, b.X)-pad, min(a.Y, b.Y)-pad, max(a.X, b.X)+pad+1, max(a.Y, b.Y)+pad+1)
	return box.Overlaps(r)
}

func distToSegment(x, y float64, a, b image.Point) float64 {
	ax, ay, bx, by := float64(a.X), float64(a.Y), float64(b.X), float64(b.Y)
	dx, dy := bx-ax, by-ay
	t := 0.0
	if l := dx*dx + dy*dy; l > 0 {
		t = math.Max(0, math.Min(1, ((x-ax)*dx+(y-ay)*dy)/l))
	}
	return math.Hypot(x-(ax+t*dx), y-(ay+t*dy))
}

// fill tints the inside of rings, taken together (even-odd), so neighboring zones of one alert are
// one even tint rather than darker where they meet.
func (pn pen) fill(rings [][]image.Point, c color.RGBA, k float64) {
	b := pn.dst.Rect
	var xs []float64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		fy := float64(y) + 0.5
		xs = xs[:0]
		for _, r := range rings {
			for i := range r {
				p, q := r[i], r[(i+1)%len(r)]
				if (float64(p.Y) <= fy) != (float64(q.Y) <= fy) {
					t := (fy - float64(p.Y)) / float64(q.Y-p.Y)
					xs = append(xs, float64(p.X)+t*float64(q.X-p.X))
				}
			}
		}
		sort.Float64s(xs)
		for i := 0; i+1 < len(xs); i += 2 {
			x0, x1 := max(int(math.Ceil(xs[i]-0.5)), b.Min.X), min(int(math.Floor(xs[i+1]-0.5)), b.Max.X-1)
			for x := x0; x <= x1; x++ {
				pn.blend(x, y, c, k)
			}
		}
	}
}

// inkOn is the text color that reads on a pill of color c: dark on the light ones, white on the dark.
func inkOn(c color.RGBA) color.RGBA {
	if 299*int(c.R)+587*int(c.G)+114*int(c.B) > 150*1000 {
		return color.RGBA{20, 20, 20, 255}
	}
	return color.RGBA{255, 255, 255, 255}
}

// alertWhen is when an alert is in force, as its page says it: "From Sat 6:00 PM until Sun 4:00 AM"
// for one still to come, "Until 2:47 PM" for one under way, "In force" when the NWS gives no end.
// A day is named when it is not today.
func alertWhen(a home.Alert, now time.Time) string {
	at := func(t time.Time) string {
		t = t.Local()
		if y, m, d := t.Date(); y != now.Year() || m != now.Month() || d != now.Day() {
			return t.Format("Mon") + " " + clockText(t)
		}
		return clockText(t)
	}
	switch {
	case !a.Onset.IsZero() && a.Onset.After(now) && !a.Ends.IsZero():
		return "From " + at(a.Onset) + " until " + at(a.Ends)
	case !a.Onset.IsZero() && a.Onset.After(now):
		return "From " + at(a.Onset)
	case !a.Ends.IsZero():
		return "Until " + at(a.Ends)
	}
	return "In force"
}

// itoa is a count as the pages write it.
func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
