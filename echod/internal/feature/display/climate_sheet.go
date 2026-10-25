//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"log/slog"
	"slices"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// The thermostat sheet: a tap on a thermostat's tile on the drawn dashboard, when its card sets no
// tap of its own, brings up its controls over the page, as Home Assistant's own does: the temperature
// where it is, the one it is set to with a step down and a step up (the low and the high of its range,
// when it keeps between two), and its modes to choose from. Done, or a tap beside the sheet, puts it
// away.

// What a part of the thermostat sheet is, for a tap.
const (
	climatePartNone = iota
	climatePartDone
	climatePartDown // the one target, or the low of a range
	climatePartUp
	climatePartHighDown // the high of a range
	climatePartHighUp
	climatePartMode
)

// climateSheet is the sheet while it is up: the thermostat, and what was set from here ahead of Home
// Assistant saying so.
type climateSheet struct {
	entity string
	// low, high and mode are what was last set from here; set says which of them were.
	low, high float64
	mode      string
	setTarget bool
}

// climateView is the sheet as one frame draws it: what the thermostat is set to now, with what was
// set from here over it.
type climateView struct {
	now dashboard.ClimateNow
}

// climateWords are the sheet's words, in the screen's language: what it is now, the range's two ends,
// Done, and the modes. Empty is English.
var climateWords = map[string]map[string]string{
	"": {"now": "Now", "low": "Heat to", "high": "Cool to", "done": "Done",
		"off": "Off", "heat": "Heat", "cool": "Cool", "heat_cool": "Heat/Cool", "auto": "Auto", "dry": "Dry", "fan_only": "Fan"},
	"de": {"now": "Jetzt", "low": "Heizen auf", "high": "Kühlen auf", "done": "Fertig",
		"off": "Aus", "heat": "Heizen", "cool": "Kühlen", "heat_cool": "Heizen/Kühlen", "auto": "Auto", "dry": "Trocknen", "fan_only": "Lüfter"},
	"es": {"now": "Ahora", "low": "Calentar a", "high": "Enfriar a", "done": "Listo",
		"off": "Apagado", "heat": "Calor", "cool": "Frío", "heat_cool": "Calor/Frío", "auto": "Auto", "dry": "Seco", "fan_only": "Ventilador"},
	"fr": {"now": "Actuel", "low": "Chauffer à", "high": "Refroidir à", "done": "OK",
		"off": "Arrêt", "heat": "Chauffage", "cool": "Clim", "heat_cool": "Chaud/Froid", "auto": "Auto", "dry": "Déshumidif.", "fan_only": "Ventilation"},
	"it": {"now": "Ora", "low": "Riscalda a", "high": "Raffresca a", "done": "Fatto",
		"off": "Spento", "heat": "Caldo", "cool": "Freddo", "heat_cool": "Caldo/Freddo", "auto": "Auto", "dry": "Deumidifica", "fan_only": "Ventola"},
	"pt": {"now": "Agora", "low": "Aquecer até", "high": "Resfriar até", "done": "Pronto",
		"off": "Desligado", "heat": "Aquecer", "cool": "Resfriar", "heat_cool": "Aquecer/Resfriar", "auto": "Auto", "dry": "Desumidificar", "fan_only": "Ventilador"},
	"ru": {"now": "Сейчас", "low": "Нагрев до", "high": "Охлаждение до", "done": "Готово",
		"off": "Выкл.", "heat": "Нагрев", "cool": "Охлаждение", "heat_cool": "Нагрев/Охл.", "auto": "Авто", "dry": "Осушение", "fan_only": "Вентилятор"},
}

// climateWord is one of the sheet's words; a mode it has no word for is shown as Home Assistant
// names it.
func climateWord(key string) string {
	w, ok := climateWords[screenLang()]
	if !ok {
		w = climateWords[""]
	}
	if s, ok := w[key]; ok {
		return s
	}
	if s, ok := climateWords[""][key]; ok {
		return s
	}
	return key
}

// climateSet is how the sheet's steps and modes reach Home Assistant, by the dashboard whose sheet it
// is (sheets); variables so that a test can see what they ask for.
var (
	climateSet  = func(entity string, low, high float64) { sheets().SetClimate(entity, low, high) }
	climateMode = func(entity, mode string) { sheets().SetHVACMode(entity, mode) }
	climateNow  = func(entity string) (dashboard.ClimateNow, bool) { return sheets().ClimateNow(entity) }
)

// climatePress is a tap on a thermostat's tile whose card sets no tap of its own: its sheet.
func (d *Display) climatePress(t *dashTile, _ int) {
	d.openClimate(t.entity)
}

// openClimate puts the thermostat sheet up for entity.
func (d *Display) openClimate(entity string) bool {
	if _, ok := climateNow(entity); !ok {
		return false
	}
	slog.Info("dashboard thermostat sheet", "entity", entity)
	d.mu.Lock()
	d.dashClimate = &climateSheet{entity: entity}
	d.mu.Unlock()
	d.wake()
	return true
}

// climateViewNow is the sheet with what its thermostat is set to now, for a frame; nil without a
// sheet.
func (d *Display) climateViewNow() *climateView {
	d.mu.Lock()
	c := d.dashClimate
	d.mu.Unlock()
	if c == nil {
		return nil
	}
	now, _ := climateNow(c.entity)
	return &climateView{now: c.over(now)}
}

// over is now with what was set from here put over it.
func (c climateSheet) over(now dashboard.ClimateNow) dashboard.ClimateNow {
	if c.mode != "" {
		now.State = c.mode
	}
	if c.setTarget {
		if now.HasRange {
			now.Low, now.High = c.low, c.high
		} else {
			now.Target, now.HasTarget = c.low, true
		}
	}
	return now
}

// climateTap is a finger lifted at x, y while the thermostat sheet is up, which the sheet always
// takes. It says whether the sheet was up.
func (d *Display) climateTap(x, y int) bool {
	d.mu.Lock()
	c := d.dashClimate
	d.mu.Unlock()
	if c == nil || d.r == nil {
		return false
	}
	now, _ := climateNow(c.entity)
	now = c.over(now)
	z, in := d.r.climateAt(x, y)
	step := func(low, high int) {
		lo, hi := now.Target, 0.0
		if now.HasRange {
			lo, hi = now.Stepped(now.Low, low), now.Stepped(now.High, high)
			if lo > hi {
				return // the two ends never cross
			}
		} else {
			lo = now.Stepped(lo, low)
		}
		slog.Info("dashboard thermostat", "entity", c.entity, "low", lo, "high", hi)
		climateSet(c.entity, lo, hi)
		d.mu.Lock()
		if d.dashClimate != nil && d.dashClimate.entity == c.entity {
			n := *d.dashClimate
			n.low, n.high, n.setTarget = lo, hi, true
			d.dashClimate = &n
		}
		d.mu.Unlock()
	}
	switch {
	case !in || z.kind == climatePartDone:
		d.mu.Lock()
		d.dashClimate = nil
		d.mu.Unlock()
	case z.kind == climatePartDown:
		step(-1, 0)
	case z.kind == climatePartUp:
		step(1, 0)
	case z.kind == climatePartHighDown:
		step(0, -1)
	case z.kind == climatePartHighUp:
		step(0, 1)
	case z.kind == climatePartMode && z.index < len(now.Modes):
		mode := now.Modes[z.index]
		slog.Info("dashboard thermostat", "entity", c.entity, "mode", mode)
		climateMode(c.entity, mode)
		d.mu.Lock()
		if d.dashClimate != nil && d.dashClimate.entity == c.entity {
			n := *d.dashClimate
			n.mode = mode
			d.dashClimate = &n
		}
		d.mu.Unlock()
	}
	d.wake()
	return true
}

// climateSheet draws the thermostat sheet over the drawn dashboard and keeps where its parts are for
// climateAt.
func (r *renderer) climateSheet(v *climateView, th dashboard.Theme) {
	if v == nil {
		r.setClimate(nil, image.Rectangle{})
		return
	}
	c := v.now
	pal := r.palette(th)
	fc := r.faces()
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(color.RGBA{0, 0, 0, 150}), image.Point{}, draw.Over)

	pad, gap := r.s(18), r.s(10)
	titleH, subH, rowH, chipH, btnH := r.s(34), r.s(28), r.s(64), r.s(44), r.s(46)
	w := min(r.w-2*r.s(24), r.s(640))
	inner := w - 2*pad
	cols := 3
	chipW := (inner - gap*(cols-1)) / cols
	rows := 0
	switch {
	case c.HasRange:
		rows = 2
	case c.HasTarget:
		rows = 1
	}
	modeRows := (len(c.Modes) + cols - 1) / cols
	h := pad + titleH + subH + rows*(rowH+gap) + modeRows*(chipH+gap) + btnH + pad
	x0, y0 := (r.w-w)/2, max((r.h-h)/2, r.s(8))
	card := image.Rect(x0, y0, x0+w, y0+h)
	r.roundFill(card, pal.rad, pal.card, pal.card)

	var zones []mediaZone
	y := y0 + pad
	name := c.Name
	if name == "" {
		name = c.Entity
	}
	r.text(fc.labelBold, r.fit(fc.labelBold, name, inner), x0+pad, y+r.s(26), pal.text)
	y += titleH
	line := climateWord(c.State)
	if c.HasCurrent {
		line = climateWord("now") + " " + trimTemp(c.Current) + " · " + line
	}
	r.text(fc.sub, r.fit(fc.sub, line, inner), x0+pad, y+r.s(20), pal.sub)
	y += subH

	// A target with a step down and a step up either side of it.
	target := func(label string, v float64, down, up int) {
		bs := rowH
		fill := lerp(pal.card, pal.text, 0.12)
		bd := image.Rect(x0+w-pad-2*bs-gap, y, x0+w-pad-bs-gap, y+bs)
		bu := image.Rect(x0+w-pad-bs, y, x0+w-pad, y+bs)
		for _, b := range []struct {
			r    image.Rectangle
			icon string
			kind int
		}{{bd, "minus", down}, {bu, "plus", up}} {
			r.roundFill(b.r, float64(bs)/2, fill, fill)
			r.mdiIcon(b.icon, b.r.Min.X+(bs-r.s(32))/2, b.r.Min.Y+(bs-r.s(32))/2, 32, pal.text)
			zones = append(zones, mediaZone{r: b.r, kind: b.kind})
		}
		tx := x0 + pad
		if label != "" {
			r.text(fc.label, label, tx, y+bs/2+r.s(9), pal.sub)
			tx += r.width(fc.label, label) + r.s(16)
		}
		r.text(fc.header, trimTemp(v), tx, y+bs/2+r.s(12), pal.text)
	}
	switch {
	case c.HasRange:
		target(climateWord("low"), c.Low, climatePartDown, climatePartUp)
		y += rowH + gap
		target(climateWord("high"), c.High, climatePartHighDown, climatePartHighUp)
		y += rowH + gap
	case c.HasTarget:
		target("", c.Target, climatePartDown, climatePartUp)
		y += rowH + gap
	}

	// The modes, the one it is in lit.
	for i, m := range c.Modes {
		cx := x0 + pad + (i%cols)*(chipW+gap)
		cy := y + (i/cols)*(chipH+gap)
		b := image.Rect(cx, cy, cx+chipW, cy+chipH)
		fill, fg := lerp(pal.card, pal.text, 0.1), pal.text
		if m == c.State {
			fill, fg = pal.accent, pal.bg
		}
		r.roundFill(b, pal.rad*0.6, fill, fill)
		word := r.fit(fc.sub, climateWord(m), chipW-r.s(16))
		r.text(fc.sub, word, b.Min.X+(chipW-r.width(fc.sub, word))/2, b.Min.Y+chipH/2+r.s(7), fg)
		zones = append(zones, mediaZone{r: b, kind: climatePartMode, index: i})
	}

	done := climateWord("done")
	bw := max(r.width(fc.button, done)+2*r.s(28), r.s(140))
	btn := image.Rect(x0+w-pad-bw, y0+h-pad-btnH, x0+w-pad, y0+h-pad)
	r.roundFill(btn, pal.rad, pal.accent, pal.accent)
	r.text(fc.button, done, btn.Min.X+(bw-r.width(fc.button, done))/2, btn.Min.Y+btnH/2+r.s(9), pal.bg)
	zones = append(zones, mediaZone{r: btn, kind: climatePartDone})
	r.setClimate(zones, card)
}

// trimTemp is a temperature as the sheet shows it: no ".0", and the degree sign.
func trimTemp(v float64) string {
	return dashboard.Adjust{Kind: "temperature"}.Label(v)
}

// climateParts is the thermostat sheet's parts and the sheet itself where they were last drawn,
// under zmu: empty while no sheet is up.
type climateParts struct {
	climateZones []mediaZone
	climateCard  image.Rectangle
}

// setClimate keeps the thermostat sheet's parts where they were drawn, for the touch goroutine.
func (r *renderer) setClimate(zones []mediaZone, card image.Rectangle) {
	r.zmu.Lock()
	r.climateZones, r.climateCard = zones, card
	r.zmu.Unlock()
}

// climateAt is the part of the thermostat sheet under x, y, and whether that is on the sheet at all.
func (r *renderer) climateAt(x, y int) (mediaZone, bool) {
	r.zmu.Lock()
	zones, card := slices.Clone(r.climateZones), r.climateCard
	r.zmu.Unlock()
	z, _, in := mediaHit(zones, card, x, y)
	return z, in
}
