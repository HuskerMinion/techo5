//go:build !dot

package display

import (
	"image"
	"strings"

	"golang.org/x/image/font"
)

// A settings row on the round panel has the width of a small card and the words of any language, so a
// row does not give up its ends at the first line that does not fit. It tries, in this order: the line
// as it is; the label and the value a step smaller; the label and the line under it on two lines each,
// which makes the row taller; and only then a real ellipsis from clipRunes, on the last line that has
// words left over. The value, which says what is set, keeps its share of the row before the words take
// the rest, so it is never cut down to a letter. The Show's rows are not planned: the card is wide
// enough there, and they keep the one-line cut of rowWords.

// rowPlan is how one row of the round panel is laid out.
type rowPlan struct {
	label, sub                    []string
	labelFace, subFace, valueFace font.Face
	value                         string // the value as drawn, cut to its share
	h                             int    // the row's height

	// baseL and baseS are the baselines of the lines of the label and of the line under it, from the
	// row's top, when the row has more than one line of words; nil for the usual row, whose baselines
	// settingRow sets by the text size.
	baseL, baseS []int
}

// planned says whether rows are planned for the width of the card they are on (a round panel's).
func (p *paint) planned() bool { return !p.rowCard.Empty() }

// controlBase is the part of a row's control that is not its value, from the right: where the value
// and the words end.
func (r *paint) controlBase(row settingRow) int {
	fc := r.faces()
	switch row.kind {
	case ctlToggle:
		return r.s(66)
	case ctlChoice:
		w := r.s(58)
		if row.button != "" {
			w += r.width(fc.button, row.button) + r.s(44+12)
		}
		return w
	case ctlButton, ctlDanger:
		return r.width(fc.button, row.button) + r.s(44)
	case ctlStepper, ctlDays:
		return r.controlWidth(row)
	}
	return 0
}

// separateValue is whether a row's value stands beside its control as words, rather than in it.
func separateValue(row settingRow) bool {
	switch row.kind {
	case ctlValue, ctlToggle, ctlChoice, ctlButton, ctlDanger:
		return row.value != ""
	}
	return false
}

// wrapClip is s on at most maxLines lines of room each, the last cut with an ellipsis if words are
// left over; a word too wide for a line on its own is cut too.
func (r *paint) wrapClip(face font.Face, s string, room, maxLines int) []string {
	lines := r.wrap(face, s, room)
	if len(lines) > maxLines {
		lines = append(lines[:maxLines-1], strings.Join(lines[maxLines-1:], " "))
	}
	for i, l := range lines {
		lines[i] = r.fit(face, l, room)
	}
	return lines
}

// planRow lays out row on card. Only rows with words are planned (not headings).
func (r *paint) planRow(card image.Rectangle, row settingRow) rowPlan {
	fc := r.faces()
	face, small := fc.label, fc.labelSm
	if row.bold {
		face, small = fc.labelBold, fc.labelBoldSm
	}
	if small == nil {
		small = face
	}
	vface, vsmall := fc.value, fc.valueSm
	if vsmall == nil {
		vsmall = vface
	}
	left, right := card.Min.X+r.rowIn(), card.Max.X-r.s(26)
	gap := r.s(rowGap)
	if row.kind == ctlDays {
		gap = r.s(8) // the days are most of the row; the name is short
	}
	avail := right - left - r.controlBase(row) - gap
	if row.locked {
		avail -= r.s(lockGap + lockW)
	}

	p := rowPlan{labelFace: face, subFace: fc.sub, valueFace: vface}
	words := avail
	if separateValue(row) {
		vw := r.width(vface, row.value)
		share := min(vw, max(avail*2/5, avail-r.width(face, row.label)-r.s(12)))
		if vw > share && r.width(vsmall, row.value) <= share {
			p.valueFace = vsmall
		}
		p.value = r.fit(p.valueFace, row.value, share)
		words = avail - r.width(p.valueFace, p.value) - r.s(12)
	}
	words = max(words, r.s(24))

	p.labelFace, p.label = face, []string{row.label}
	if r.width(face, row.label) > words {
		if r.width(small, row.label) <= words {
			p.labelFace = small
		} else {
			if len(r.wrap(face, row.label, words)) > 2 {
				p.labelFace = small
			}
			p.label = r.wrapClip(p.labelFace, row.label, words, 2)
		}
	}
	if row.sub != "" {
		p.sub = r.wrapClip(fc.sub, row.sub, words, 2)
	}

	p.h = r.rowH()
	if len(p.label) == 1 && len(p.sub) <= 1 {
		return p
	}
	lm, sm := p.labelFace.Metrics(), fc.sub.Metrics()
	lhL, lhS, between := lm.Height.Ceil(), sm.Height.Ceil(), r.s(2)
	total := len(p.label)*lhL + len(p.sub)*lhS
	if len(p.sub) > 0 {
		total += between
	}
	p.h = max(p.h, total+2*r.s(10))
	y := (p.h - total) / 2
	for i := range p.label {
		p.baseL = append(p.baseL, y+i*lhL+lm.Ascent.Ceil())
	}
	y += len(p.label)*lhL + between
	for i := range p.sub {
		p.baseS = append(p.baseS, y+i*lhS+sm.Ascent.Ceil())
	}
	return p
}
