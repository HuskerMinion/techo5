//go:build spot

package display

import (
	"image"
	"strings"
	"testing"
)

// A row on the round card keeps every line of its words and its value inside the card, shrinks and
// wraps before it cuts, and grows to hold what it wraps.
func TestRoundRowsWrapBeforeTheyCut(t *testing.T) {
	r := newRoundRenderer(image.NewRGBA(image.Rect(0, 0, side, side)))
	card := r.rowCard
	left, right := card.Min.X+r.rowIn(), card.Max.X-r.s(26)
	long := "Escluso dai comandi per area di Home Assistant e dalle scene"
	for _, row := range []settingRow{
		{label: "Ignora le luci della stanza", sub: long, kind: ctlToggle},
		{label: "Voce per le risposte", sub: "La voce con cui risponde", kind: ctlChoice, value: "Italiano (Isabella, neurale)"},
		{label: "Connessione a Home Assistant", sub: long, kind: ctlValue, value: "Connesso"},
		{label: "Volume pieno dal primo secondo del suono", kind: ctlStepper, value: "5"},
		{label: "Riavvia", sub: "Di nuovo disponibile tra circa un minuto", kind: ctlButton, button: "Riavvia"},
		{label: "Days", kind: ctlDays},
	} {
		p := r.planRow(card, row)
		room := right - left - r.controlBase(row)
		for _, l := range p.label {
			if w := r.width(p.labelFace, l); w > room {
				t.Errorf("%q: line %q is %d wide, the row has %d", row.label, l, w, room)
			}
		}
		for _, l := range p.sub {
			if w := r.width(p.subFace, l); w > room {
				t.Errorf("%q: line %q is %d wide, the row has %d", row.label, l, w, room)
			}
		}
		if p.value != "" && r.width(p.valueFace, p.value) > room {
			t.Errorf("%q: value %q is too wide", row.label, p.value)
		}
		if row.value != "" && separateValue(row) && len([]rune(p.value)) < 4 {
			t.Errorf("%q: value cut to %q", row.label, p.value)
		}
		if len(p.label) > 2 || len(p.sub) > 2 {
			t.Errorf("%q: %d and %d lines", row.label, len(p.label), len(p.sub))
		}
		if len(p.label) > 1 || len(p.sub) > 1 {
			if p.h <= r.rowH() || p.baseL == nil {
				t.Errorf("%q: wrapped but the row is %d tall", row.label, p.h)
			}
		}
		for _, l := range p.sub {
			if strings.Count(l, "…") > 1 {
				t.Errorf("%q: %q", row.label, l)
			}
		}
		// Drawing it must stay inside the card and the list.
		r.settingRow(card, 100, row)
	}
	if got := r.planRow(card, settingRow{label: "Name", sub: "Short", kind: ctlValue, value: "Echo"}); got.h != r.rowH() || got.baseL != nil {
		t.Errorf("a row that fits changed: %+v", got)
	}
}
