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

	// A rule, then a drawn play or pause mark with what a tap does, well clear of it.
	draw.Draw(r.dst, image.Rect(r.margin, r.h-84, r.w-r.margin, r.h-81), image.NewUniform(ember), image.Point{}, draw.Src)
	hint := "tap to pause"
	x, top := r.margin+6, r.h-62
	if s.paused {
		hint = "tap to play"
		// A triangle, drawn as rows.
		for i := 0; i < 36; i++ {
			half := i
			if i > 18 {
				half = 36 - i
			}
			draw.Draw(r.dst, image.Rect(x, top+i, x+half*3/2+1, top+i+1), image.NewUniform(amber), image.Point{}, draw.Src)
		}
	} else {
		draw.Draw(r.dst, image.Rect(x, top, x+11, top+36), image.NewUniform(amber), image.Point{}, draw.Src)
		draw.Draw(r.dst, image.Rect(x+19, top, x+30, top+36), image.NewUniform(amber), image.Point{}, draw.Src)
	}
	r.text(r.small, hint, r.margin+80, r.h-32, dim)
}

// background paints the picture behind the now-playing text, toned down so the text reads.
func (r *renderer) background(rd home.Radio) {
	if rd.Art != nil {
		// Over the ground, so a logo's empty surround stays the theme's color; then a wash of the
		// ground color: covers stay recognisable, logos sit back, text stays legible.
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
