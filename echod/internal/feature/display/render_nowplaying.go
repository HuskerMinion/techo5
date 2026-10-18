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
	r.text(r.small, label, r.margin, r.s(150), amber)
	if s.hasLyric && rd.Title != "" {
		r.lyricLines(s.lyric, rd)
	} else {
		r.songLines(headline, sub)
	}

	r.transportRow(s)
}

// songLines is the middle of the page without words: the song (or the station) large, who plays it under.
func (r *renderer) songLines(headline, sub string) {
	face := r.title
	if r.width(face, headline) > r.w-2*r.margin {
		face = r.body
	}
	y := r.s(225)
	for i, line := range r.wrap(face, headline, r.w-2*r.margin) {
		if i == 2 {
			break
		}
		r.text(face, line, r.margin, y, cream)
		y += r.s(56)
	}
	if sub != "" {
		r.text(r.body, sub, r.margin, y+r.s(6), dim)
	}
}

// lyricLines is the middle of the page with the words: the song and who plays it on one line, the line
// being sung large under it, and the next one dim. Before the first line and in a long gap the large
// line is a mark, so the page does not look stuck.
func (r *renderer) lyricLines(l home.Lyric, rd home.Radio) {
	width := r.w - 2*r.margin
	song := rd.Title
	if rd.Artist != "" {
		song += "  ·  " + rd.Artist
	}
	r.text(r.body, r.clipTo(r.body, song, width), r.margin, r.s(200), dim)

	line := l.Line
	color := cream
	if line == "" {
		line, color = "· · ·", dim
	}
	face := r.title
	if r.width(face, line) > width && len(r.wrap(face, line, width)) > 2 {
		face = r.body
	}
	y := r.s(268)
	for i, part := range r.wrap(face, line, width) {
		if i == 2 {
			break
		}
		r.text(face, part, r.margin, y, color)
		y += r.s(54)
	}
	if l.Next != "" {
		r.text(r.body, r.clipTo(r.body, l.Next, width), r.margin, max(y, r.s(330))+r.s(6), dim)
	}
}

// transportRow is the foot of the page: the rule, back, play or pause, forward, Done and the star.
func (r *renderer) transportRow(s scene) {
	// The three buttons: back, play or pause, and forward. What they do belongs to whoever is playing, so
	// a stream Music Assistant is carrying is paused and skipped by the server. A rule used to run across
	// here as well; the buttons' own rim and shadow separate the footer now, and it read as clutter.
	back, play, next := r.transportButtons()
	r.transportButton(back, "skip-previous", false)
	if s.paused {
		r.transportButton(play, "play", true)
	} else {
		r.transportButton(play, "pause", true)
	}
	r.transportButton(next, "skip-next", false)

	// Done, in the corner the buttons leave: the music ends and the screen goes back to the clock. A
	// pause keeps this page up with play on it, and so does a stop from Music Assistant, which looks
	// the same from here, so the page needs its own way out.
	done := r.doneButton()
	doneCol := r.buttonFace(done, float64(done.Dy())/2, btnSecondary)
	// The sheet's button face, not this page's small text: a button drawn from one place has to be
	// labeled from one place, or the same control is two sizes on two screens. The baseline is the
	// sheet's too, from pillButton.
	doneFace := r.faces().button
	r.text(doneFace, "Done", done.Min.X+(done.Dx()-r.width(doneFace, "Done"))/2, done.Min.Y+done.Dy()/2+r.s(8), doneCol)

	// The star saves what is playing to favorites, and fills in once it has.
	fav := r.favButton()
	r.buttonFace(fav, float64(fav.Dy())/2, btnSecondary)
	// The icon font's star, not one drawn as a polygon: the same outline the rest of the program would
	// draw, and filled in once the station is saved.
	starIcon := "star-outline"
	starColor := color.RGBA{0x9a, 0x8c, 0x7a, 0xff}
	if s.faved {
		starIcon, starColor = "star", amber
	}
	r.iconIn(fav, starIcon, transportIcon, starColor)
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

// transportIcon is the size a transport icon is drawn at, the size the media sheet draws these same
// controls at: small enough for the strip's circles and the page's pills both. closeIcon is the cross on
// the strip's way out, a smaller button than the three.
const (
	transportIcon = 32
	closeIcon     = 20
)

// transportButton draws one of the transport controls: the button the rest of the program draws, in the
// accent for the one in the middle, with the icon font's mark for that control. The names are the media
// sheet's, so the two screens show the same three buttons — and a glyph from the font is antialiased at
// any size, which the rows this page used to paint its own marks from were not.
func (r *renderer) transportButton(b image.Rectangle, icon string, accent bool) {
	style := btnSecondary
	if accent {
		style = btnPrimary
	}
	col := r.buttonFace(b, float64(b.Dy())/2, style)
	r.iconIn(b, icon, transportIcon, col)
}

// iconIn draws an icon-font glyph centered in b: the one place the transport marks, the star and the
// strip's cross take their position from. mdiIconIn does it by the glyph's own ink, which is the only
// thing that works for icons whose ink does not sit where a font's baseline puts text.
func (r *renderer) iconIn(b image.Rectangle, name string, size int, col color.RGBA) {
	r.mdiIconIn(name, b, size, col)
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
		draw.Draw(r.dst, r.dst.Rect, image.NewUniform(washOf(walnut, alpha)), image.Point{}, draw.Over)
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
