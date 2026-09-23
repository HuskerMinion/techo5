//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"time"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The music strip: once music has played a while, the Show goes back to its clock - photos and all -
// with the music in a strip across the foot: the song, back, play or pause, forward, and an X that
// stops it. A tap on the song brings the full page back for a while. How soon, or never, is a setting.

// setMusicStrip is the settings screen's choice.
func (d *Display) setMusicStrip(i int) { setStrip(d.strip, i) }

// stripDue reports whether the music goes in the strip rather than on the full page: a page put away
// by a swipe always does when the strip is on, and a page on the screen does once the setting's time
// has gone by for the track. Called with d.mu held.
func (d *Display) stripDue(now time.Time, rd home.Radio, page bool) bool {
	secs := config.Get().Screen.MusicStrip
	if secs <= 0 {
		return false
	}
	if key := rd.Title + "\x00" + rd.Now; key != d.stripKey {
		d.stripKey, d.stripSince = key, now
	}
	if !page {
		return true
	}
	if now.Before(d.stripFullUntil) {
		return false
	}
	return now.Sub(d.stripSince) >= time.Duration(secs)*time.Second
}

// stripRect is the strip, across the foot of the panel.
func (r *renderer) stripRect() image.Rectangle {
	return image.Rect(r.s(24), r.h-r.s(108), r.w-r.s(24), r.h-r.s(14))
}

// stripButtons are the strip's controls: back, play or pause, forward, and the X at its right.
func (r *renderer) stripButtons() (back, play, next, closeX image.Rectangle) {
	b := r.stripRect()
	cy := (b.Min.Y + b.Max.Y) / 2
	rad := r.s(33)
	at := func(cx int) image.Rectangle { return image.Rect(cx-rad, cy-rad, cx+rad, cy+rad) }
	mid := b.Min.X + b.Dx()*5/8
	gap := r.s(86)
	back, play, next = at(mid-gap), at(mid), at(mid+gap)
	cr := r.s(17)
	cx, ccy := b.Max.X-r.s(26), b.Min.Y+r.s(24)
	closeX = image.Rect(cx-cr, ccy-cr, cx+cr, ccy+cr)
	return back, play, next, closeX
}

// stripSong is the strip's picture and words: a tap there brings the full page back.
func (r *renderer) stripSong() image.Rectangle {
	b := r.stripRect()
	back, _, _, _ := r.stripButtons()
	return image.Rect(b.Min.X, b.Min.Y, back.Min.X-r.s(8), b.Max.Y)
}

// musicStrip draws the strip over whatever the clock page has under it.
func (r *renderer) musicStrip(s scene) {
	rd := s.radio
	b := r.stripRect()
	rad := float64(r.s(16))
	r.roundFill(b, rad, shift(walnut, 6), shift(walnut, 2))
	r.roundStroke(b, rad, 2, ember)

	// The picture: the song's cover or the station's logo when there is one, notes when not.
	pad := r.s(12)
	art := image.Rect(b.Min.X+pad, b.Min.Y+pad, b.Min.X+pad+b.Dy()-2*pad, b.Max.Y-pad)
	r.roundFill(art, float64(r.s(8)), shift(walnut, 18), shift(walnut, 14))
	if pic := stripPicture(rd); pic != nil {
		xdraw.ApproxBiLinear.Scale(r.dst, art.Inset(r.s(2)), pic, pic.Bounds(), draw.Over, nil)
	} else {
		note := "♪"
		r.text(r.title, note, art.Min.X+(art.Dx()-r.width(r.title, note))/2, art.Max.Y-r.s(16), amber)
	}

	// Two lines: the song, then who and from where. Paused says so in place of the source.
	station := rd.Now
	if station == "" {
		station = rd.Chosen
	}
	headline, sub := rd.Title, rd.Artist
	if headline == "" {
		headline, sub = station, ""
	} else if station != "" {
		sub = joinDot(sub, station)
	}
	if s.paused {
		sub = joinDot("Paused", sub)
	}
	back, play, next, closeX := r.stripButtons()
	tx := art.Max.X + r.s(14)
	room := back.Min.X - r.s(12) - tx
	r.text(r.small, r.clipTo(r.small, headline, room), tx, b.Min.Y+r.s(42), cream)
	if sub != "" {
		r.text(r.tiny, r.clipTo(r.tiny, sub, room), tx, b.Min.Y+r.s(76), amber)
	}

	// The controls see through to the strip, so the song under them stays readable.
	shade := color.NRGBA{R: 16, G: 12, B: 10, A: 150}
	for _, c := range []image.Rectangle{back, play, next} {
		r.disc(c.Min.X+c.Dx()/2, c.Min.Y+c.Dy()/2, c.Dx()/2, shade)
	}
	r.markBack(back.Min.X+back.Dx()/2-r.s(22), back.Min.Y+back.Dy()/2-r.s(18))
	if s.paused {
		r.markPlay(play.Min.X+play.Dx()/2-r.s(12), play.Min.Y+play.Dy()/2-r.s(18))
	} else {
		r.markPause(play.Min.X+play.Dx()/2-r.s(15), play.Min.Y+play.Dy()/2-r.s(18))
	}
	r.markNext(next.Min.X+next.Dx()/2-r.s(27), next.Min.Y+next.Dy()/2-r.s(18))

	cx, cy, cr := closeX.Min.X+closeX.Dx()/2, closeX.Min.Y+closeX.Dy()/2, closeX.Dx()/2
	r.disc(cx, cy, cr, color.NRGBA{R: 16, G: 12, B: 10, A: 230})
	k := r.s(6)
	r.stroke(cx-k, cy-k, cx+k, cy+k, r.s(3), cream)
	r.stroke(cx-k, cy+k, cx+k, cy-k, r.s(3), cream)
}

// joinDot joins two parts of a line with a dot, leaving out an empty one.
func joinDot(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "  ·  " + b
}

// stripPicture is the song's cover, or the station's logo, or nothing.
func stripPicture(rd home.Radio) image.Image {
	if rd.Thumb != nil {
		return rd.Thumb
	}
	if rd.Art != nil {
		return rd.Art
	}
	return nil
}

// clipTo shortens s to fit w with an ellipsis.
func (r *renderer) clipTo(face font.Face, s string, w int) string {
	if r.width(face, s) <= w {
		return s
	}
	rs := []rune(s)
	for len(rs) > 1 && r.width(face, string(rs)+"…") > w {
		rs = rs[:len(rs)-1]
	}
	return string(rs) + "…"
}

// star fills a five-pointed star centered at cx, cy with outer radius rad.
func (r *renderer) star(cx, cy, rad int, c color.Color) {
	var pts [10][2]float64
	for i := range pts {
		a := -math.Pi/2 + float64(i)*math.Pi/5
		rr := float64(rad)
		if i%2 == 1 {
			rr *= 0.45
		}
		pts[i] = [2]float64{float64(cx) + rr*math.Cos(a), float64(cy) + rr*math.Sin(a)}
	}
	src := image.NewUniform(c)
	for y := cy - rad; y <= cy+rad; y++ {
		for x := cx - rad; x <= cx+rad; x++ {
			if inPolygon(float64(x)+0.5, float64(y)+0.5, pts[:]) {
				draw.Draw(r.dst, image.Rect(x, y, x+1, y+1), src, image.Point{}, draw.Over)
			}
		}
	}
}

// inPolygon is the even-odd test.
func inPolygon(x, y float64, pts [][2]float64) bool {
	in := false
	for i, j := 0, len(pts)-1; i < len(pts); j, i = i, i+1 {
		xi, yi, xj, yj := pts[i][0], pts[i][1], pts[j][0], pts[j][1]
		if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
			in = !in
		}
	}
	return in
}
