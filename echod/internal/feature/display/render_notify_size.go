//go:build !dot && !spot

package display

import (
	"image"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
	"github.com/HuskerMinion/techo5/echod/internal/lib/mdi"
)

// A notification asked to be large or huge (notify's size): a bigger card, and its words as large as
// the card takes them, so it can be read across a room.

// sizedBox is where a large or huge card sits: most of the screen, centered. The usual card is 700 by 300
// of the Show 5's pixels.
func (r *renderer) sizedBox(s notify.Size) image.Rectangle {
	w, h := r.w*82/100, r.h*62/100
	if s == notify.SizeHuge {
		w, h = r.w*95/100, r.h*90/100
	}
	return image.Rect((r.w-w)/2, (r.h-h)/2, (r.w-w)/2+w, (r.h-h)/2+h)
}

// sizedNoticeCard draws the card as noticeCard does, but with the heading larger and the message as large
// as the box holds, across as many lines as it needs; huge words are centered.
func (r *renderer) sizedNoticeCard(box image.Rectangle, s notify.Size, icon, heading, text string) {
	r.roundShadow(box, r.cardRad(), float64(r.s(34)), r.s(12), shadowAlpha()*1.3)
	r.roundFill(box, r.cardRad(), surface(4), surface(2))
	r.roundHighlight(box, r.cardRad())

	in := r.rowIn()
	x := box.Min.X + in
	y := box.Min.Y + r.s(14)
	if icon != "" || heading != "" {
		hf := r.textFace(true, 30)
		if s == notify.SizeHuge {
			hf = r.textFace(true, 38)
		}
		m := hf.Metrics()
		base := y + m.Ascent.Ceil()
		if _, ok := mdi.Rune(icon); ok {
			sz := m.Height.Ceil() * 9 / 10
			r.mdiIcon(icon, x, base-sz+r.s(4), sz, amber)
			x += sz + r.s(12)
		}
		if heading != "" {
			r.text(hf, r.fit(hf, upper(heading), box.Max.X-in-x), x, base, amber)
		}
		y += m.Height.Ceil() + r.s(10)
	}
	area := image.Rect(box.Min.X+in, y, box.Max.X-in, box.Max.Y-r.s(16))
	lines, face, lineH := r.fitWords(true, text, area.Dx(), area.Dy(), popupSizes(s))
	if face == nil {
		return
	}
	top := area.Min.Y + (area.Dy()-len(lines)*lineH)/2
	for i, line := range lines {
		lx := area.Min.X
		if s == notify.SizeHuge {
			lx += (area.Dx() - r.width(face, line)) / 2
		}
		r.text(face, line, lx, top+i*lineH+face.Metrics().Ascent.Ceil(), cream)
	}
}

// captionWords is what the camera page's caption bar draws for a notification of size s, large or huge:
// its title's face, and the message's lines and face, fitted to as much of the picture as the size
// allows.
type captionFit struct {
	titleFace font.Face
	lines     []string
	face      font.Face
	lineH     int
}

func (r *renderer) sizedCaption(title, message string, size notify.Size) captionFit {
	sizes, tpx, most := stepsDown(64, 40, 4), 28, r.h*30/100
	if size == notify.SizeHuge {
		sizes, tpx, most = stepsDown(200, 44, 8), 36, r.h*55/100
	}
	var c captionFit
	room := most - 2*r.s(captionPad)
	if title != "" {
		c.titleFace = r.textFace(true, tpx)
		room -= c.titleFace.Metrics().Height.Ceil()
	}
	c.lines, c.face, c.lineH = r.fitWords(true, message, r.w-2*r.margin, room, sizes)
	return c
}
