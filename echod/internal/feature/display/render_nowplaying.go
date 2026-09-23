//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// nowPlaying is the idle screen while the radio plays or sits paused. Behind everything is a
// picture: the song's cover when the station's service names one, the station's logo when it
// does not, and a drawn stand-in when there is neither — notes for a music station, waves for
// talk. Over it: the weather top left, the time top right, the station, the song and who plays
// it across the middle, and what a tap does at the bottom.
func (r *renderer) nowPlaying(s scene) {
	rd := s.radio
	r.background(rd)

	r.cornerClockDated(s)
	r.weatherCorner(s)

	station := rd.Now
	if station == "" {
		station = rd.Chosen
	}
	if station == "" {
		station = "Radio"
	}
	label := "Radio"
	if s.paused {
		label = "Paused"
	} else if s.playing {
		label = "Playing"
	}

	headline, sub := station, ""
	if rd.Title != "" {
		// A song: the station joins the label, the song takes the middle.
		label += "  ·  " + station
		headline, sub = rd.Title, rd.Artist
	}
	r.text(r.small, label, r.margin, 150, amber)
	face := r.title
	if r.width(face, headline) > r.w-2*r.margin {
		face = r.body
	}
	y := 225
	for i, line := range r.wrap(face, headline, r.w-2*r.margin) {
		if i == 2 {
			break
		}
		r.text(face, line, r.margin, y, cream)
		y += 56
	}
	if sub != "" {
		r.text(r.body, sub, r.margin, y+6, dim)
	}

	// A rule, then the three buttons: back, play or pause, and forward. What they do belongs to whoever
	// is playing, so a stream Music Assistant is carrying is paused and skipped by the server.
	draw.Draw(r.dst, image.Rect(r.margin, r.h-84, r.w-r.margin, r.h-81), image.NewUniform(ember), image.Point{}, draw.Src)
	back, play, next := r.transportButtons()
	r.control(back, r.markBack)
	if s.paused {
		r.control(play, r.markPlay)
	} else {
		r.control(play, r.markPause)
	}
	r.control(next, r.markNext)

	// Done, in the corner the buttons leave: the music ends and the screen goes back to the clock. A
	// pause keeps this page up with play on it, and so does a stop from Music Assistant, which looks
	// the same from here, so the page needs its own way out.
	done := r.doneButton()
	r.bevel(done, shift(ember, 16), true)
	r.text(r.small, "Done", done.Min.X+(done.Dx()-r.width(r.small, "Done"))/2, done.Min.Y+done.Dy()/2+r.s(10), amber)

	// The star saves what is playing to favorites, and fills in once it has.
	fav := r.favButton()
	r.bevel(fav, shift(ember, 16), true)
	starColor := color.RGBA{0x9a, 0x8c, 0x7a, 0xff}
	if s.faved {
		starColor = amber
	}
	r.star(fav.Min.X+fav.Dx()/2, fav.Min.Y+fav.Dy()/2, r.s(17), starColor)
}

// favButton is the star, beside Done.
func (r *renderer) favButton() image.Rectangle {
	done := r.doneButton()
	return image.Rect(done.Max.X+r.s(12), done.Min.Y, done.Max.X+r.s(12)+done.Dx(), done.Max.Y)
}

// doneButton is the now-playing screen's way out, at the foot on the left, level with the three.
func (r *renderer) doneButton() image.Rectangle {
	w, h := r.s(110), r.s(54)
	y := r.h - r.s(26) - h
	return image.Rect(r.margin, y, r.margin+w, y+h)
}

// transportButtons are the three soft buttons at the foot of the now-playing screen: back, play or pause,
// and forward. They are laid out from the middle so they sit under the text wherever it ends, and from
// the panel's own size, because this page is drawn on the Show 8 as well: a width that fits the Show 5 is
// a third of a wider screen with the marks stranded in the corner of it.
func (r *renderer) transportButtons() (back, play, next image.Rectangle) {
	w, h, gap := r.s(96), r.s(54), r.s(14)
	x := (r.w - (3*w + 2*gap)) / 2
	y := r.h - r.s(26) - h
	back = image.Rect(x, y, x+w, y+h)
	play = image.Rect(x+w+gap, y, x+2*w+gap, y+h)
	next = image.Rect(x+2*(w+gap), y, x+3*w+2*gap, y+h)
	return back, play, next
}

// control paints one of the three, with its mark in the middle.
func (r *renderer) control(b image.Rectangle, mark func(x, top int)) {
	r.bevel(b, shift(ember, 16), true)
	mark(b.Min.X+b.Dx()/2-r.s(18), b.Min.Y+r.s(9))
}

// The marks are 36 rows high from x and top at the Show 5's width, drawn as rows the way this panel has
// always drawn its play mark. Sizes go through s() so a wider panel gets marks to match its buttons.

func (r *renderer) markBack(x, top int) {
	r.markTriangle(x+r.s(14), top, r.s(30), false)
	r.markBar(x, top)
}

func (r *renderer) markNext(x, top int) {
	r.markTriangle(x, top, r.s(30), true)
	r.markBar(x+r.s(44), top)
}

func (r *renderer) markPlay(x, top int) { r.markTriangle(x, top, r.s(36), true) }

func (r *renderer) markPause(x, top int) {
	r.markBar(x, top)
	r.markBar(x+r.s(19), top)
}

func (r *renderer) markBar(x, top int) {
	mark := image.Rect(x, top, x+r.s(11), top+r.s(36))
	draw.Draw(r.dst, mark, image.NewUniform(amber), image.Point{}, draw.Src)
}

// markTriangle draws a triangle pointing right or left, widest in the middle.
func (r *renderer) markTriangle(x, top, size int, right bool) {
	for i := 0; i < size; i++ {
		half := min(i, size-1-i)
		w := half*3/2 + 1
		x0 := x
		if !right {
			x0 = x + (size/2)*3/2 + 1 - w
		}
		draw.Draw(r.dst, image.Rect(x0, top+i, x0+w, top+i+1), image.NewUniform(amber), image.Point{}, draw.Src)
	}
}

// background paints the picture behind the now-playing text, toned down so the text reads.
func (r *renderer) background(rd home.Radio) {
	if rd.Art != nil {
		// Over the ground, so a logo's empty surround stays the theme's color; then a wash of the
		// ground color: covers stay recognizable, logos sit back, text stays legible.
		draw.Draw(r.dst, r.dst.Rect, rd.Art, rd.Art.Bounds().Min, draw.Over)
		alpha := uint8(150)
		if rd.Logo {
			alpha = 120
		}
		wash := color.RGBA{walnut.R, walnut.G, walnut.B, alpha}
		draw.Draw(r.dst, r.dst.Rect, image.NewUniform(wash), image.Point{}, draw.Over)
		return
	}
	// No picture from the service: a drawn one, faint, on the right where the text is not.
	r.faded(28, func() {
		cx, cy := r.w-260, r.h/2
		if rd.Music {
			// Two notes: heads, stems and a beam.
			r.disc(cx-70, cy+90, 34, amber)
			r.disc(cx+80, cy+70, 34, amber)
			r.stroke(cx-42, cy+80, cx-42, cy-120, 14, amber)
			r.stroke(cx+108, cy+60, cx+108, cy-140, 14, amber)
			r.stroke(cx-48, cy-120, cx+114, cy-140, 30, amber)
			return
		}
		// Talk: rings going out from a transmitter dot, largest first so each ring shows.
		for _, rad := range []int{190, 130, 70} {
			r.disc(cx, cy, rad+9, amber)
			r.disc(cx, cy, rad-9, walnut)
		}
		r.disc(cx, cy, 22, amber)
	})
}
