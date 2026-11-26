//go:build !dot

package display

import (
	"image"
	"image/color"
	"strings"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// A markdown card drawn with the little structure it has (#757): its headings larger and bold, its
// paragraphs and bullets at body size, a step under a tile's name so a few lines of text don't weigh
// as much as the tiles beside them, bold words bold, a bullet with a hanging indent, and half a line
// where a blank line was.

// markedLine is a line of a card laid out: the words of each line it wraps to, its faces, and where
// its baselines fall below the card's text top.
type markedLine struct {
	kind  string
	lines [][]markedWord
	tops  []int // each wrapped line's baseline, from the top of the text
}

// markedWord is a piece of a wrapped line in one face, with the space before it already in it.
type markedWord struct {
	s    string
	bold bool
}

// markedFaces are the faces a card's text is drawn in.
type markedFaces struct {
	body, bold, h1, h2, h3 font.Face
}

func (r *paint) markedFaces() markedFaces {
	fc := r.faces()
	bold := r.textFace(true, 25)
	if bold == nil {
		bold = fc.value
	}
	h3 := bold
	return markedFaces{body: fc.value, bold: bold, h1: fc.header, h2: fc.labelBold, h3: h3}
}

// headingFace is the face for a heading of level n: # is the page's heading size, ## and ### a tile's
// name in bold, and the smaller ones the body's bold, as a browser steps them down.
func (m markedFaces) heading(level int) font.Face {
	switch {
	case level <= 1:
		return m.h1
	case level <= 3:
		return m.h2
	}
	return m.h3
}

// lineStep is a face's line: its height with a little lead, from its own metrics so that any font and
// text size spaces as it should.
func (r *paint) lineStep(face font.Face) (step, ascent int) {
	if face == nil {
		return r.s(textLineH), r.s(22)
	}
	m := face.Metrics()
	return m.Height.Ceil() + r.s(4), m.Ascent.Ceil() + r.s(2)
}

// bulletIndent is how far a bullet's words sit in from the card's text, the dot in the room before
// them.
const bulletIndent = 24

// layoutMarked lays a card's lines out w wide, and says how tall they come to.
func (r *paint) layoutMarked(lines []dashboard.TextLine, w int) ([]markedLine, int) {
	mf := r.markedFaces()
	var out []markedLine
	y := 0
	bodyStep, _ := r.lineStep(mf.body)
	for i, l := range lines {
		ml := markedLine{kind: l.Kind}
		switch l.Kind {
		case "gap":
			y += bodyStep / 2
			out = append(out, ml)
			continue
		case "heading":
			face := mf.heading(l.Level)
			if i > 0 {
				y += r.s(4) // a little air over a heading under other text
			}
			step, asc := r.lineStep(face)
			for _, s := range r.wrapLines(face, l.Plain(), w) {
				ml.lines = append(ml.lines, []markedWord{{s: s, bold: true}})
				ml.tops = append(ml.tops, y+asc)
				y += step
			}
			out = append(out, ml)
			continue
		}
		room := w
		if l.Kind == "bullet" {
			room -= r.s(bulletIndent)
		}
		_, asc := r.lineStep(mf.body)
		for _, words := range r.wrapRuns(mf, l.Runs, room) {
			ml.lines = append(ml.lines, words)
			ml.tops = append(ml.tops, y+asc)
			y += bodyStep
		}
		out = append(out, ml)
	}
	return out, y
}

// wrapRuns breaks a line of runs into lines no wider than w. A line in one weight is wrapped as any
// text is (wrapLines, which breaks a Japanese run between its characters); one that mixes weights is
// broken at its spaces, each word measured in its own face.
func (r *paint) wrapRuns(mf markedFaces, runs []dashboard.TextRun, w int) [][]markedWord {
	if len(runs) == 1 {
		face := mf.body
		if runs[0].Bold {
			face = mf.bold
		}
		var out [][]markedWord
		for _, s := range r.wrapLines(face, runs[0].Text, w) {
			out = append(out, []markedWord{{s: s, bold: runs[0].Bold}})
		}
		return out
	}
	type token struct {
		s           string
		bold, space bool // space: a space comes before it
	}
	var toks []token
	space := false
	for _, run := range runs {
		for i, f := range strings.Fields(run.Text) {
			sp := space || i > 0 || strings.HasPrefix(run.Text, " ")
			if len(toks) == 0 {
				sp = false
			}
			toks = append(toks, token{s: f, bold: run.Bold, space: sp})
		}
		space = strings.HasSuffix(run.Text, " ")
	}
	faceOf := func(bold bool) font.Face {
		if bold {
			return mf.bold
		}
		return mf.body
	}
	gap := r.width(mf.body, " ")
	var out [][]markedWord
	var line []markedWord
	x := 0
	for _, t := range toks {
		tw := r.width(faceOf(t.bold), t.s)
		lead := 0
		if t.space && len(line) > 0 {
			lead = gap
		}
		if len(line) > 0 && x+lead+tw > w {
			out = append(out, line)
			line, x, lead = nil, 0, 0
		}
		s := t.s
		if lead > 0 {
			s = " " + s
		}
		// Words in a row in one weight are one piece, drawn in one go.
		if n := len(line); n > 0 && line[n-1].bold == t.bold {
			line[n-1].s += s
		} else {
			line = append(line, markedWord{s: s, bold: t.bold})
		}
		x += lead + tw
	}
	if len(line) > 0 {
		out = append(out, line)
	}
	return out
}

// markedCard draws a markdown card's lines in a card at x, y, w wide, under its title if it has one,
// and says how tall it is. Off the screen it is measured and not drawn.
func (r *paint) markedCard(b dashboard.Block, x, y, w int, pal dashPal) int {
	fc := r.faces()
	pad := r.s(cardPad) + r.s(4)
	laid, textH := r.layoutMarked(b.Marked, w-2*pad)
	h := pad*2 + textH
	if b.Title != "" {
		h += r.s(cardTitleH)
	}
	if !r.visible(y, y+h) {
		return h
	}
	r.roundFill(image.Rect(x, y, x+w, y+h), pal.rad, pal.card, pal.card)
	ty := y + pad
	if b.Title != "" {
		r.text(fc.labelBold, r.fit(fc.labelBold, b.Title, w-2*pad), x+pad, ty+r.s(26), pal.text)
		ty += r.s(cardTitleH)
	}
	mf := r.markedFaces()
	for i, ml := range laid {
		src := b.Marked[i]
		for j, words := range ml.lines {
			base := ty + ml.tops[j]
			left := x + pad
			if ml.kind == "bullet" {
				left += r.s(bulletIndent)
				if j == 0 {
					r.bulletDot(x+pad+r.s(6), base, mf.body, pal.text)
				}
			}
			for _, wd := range words {
				face := mf.body
				switch {
				case ml.kind == "heading":
					face = mf.heading(src.Level)
				case wd.bold:
					face = mf.bold
				}
				r.text(face, wd.s, left, base, pal.text)
				left += r.width(face, wd.s)
			}
		}
	}
	return h
}

// bulletDot is a bullet's dot, round whatever the font, at the middle of a lower-case letter on the
// line whose baseline is base.
func (r *paint) bulletDot(x, base int, face font.Face, c color.RGBA) {
	d := max(r.s(7), 3)
	mid := base - r.s(8)
	if face != nil {
		if b, _, ok := face.GlyphBounds('x'); ok {
			mid = base + ((b.Min.Y + b.Max.Y) / 2).Round()
		}
	}
	box := image.Rect(x, mid-d/2, x+d, mid-d/2+d)
	r.roundFill(box, float64(d)/2, c, c)
}
