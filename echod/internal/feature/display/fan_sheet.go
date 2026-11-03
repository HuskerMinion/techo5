//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log/slog"
	"slices"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// The fan sheet: a finger resting on a fan's tile on the drawn dashboard, and lifting without sliding
// or scrolling, brings up its controls over the page, as the thermostat's does: on or off, its speed
// (a slider, on the steps the fan takes), the way it turns when it turns either way, and its preset
// modes when it has any. Each is there only when the fan has it. Done, or a tap beside the sheet,
// puts it away.

// What a part of the fan sheet is, for a tap.
const (
	fanPartNone = iota + 100 // apart from the other sheets' parts: a slider's kind is one of them
	fanPartDone
	fanPartPower
	fanPartSpeed // the slider; a finger on it sets the speed (slideSheet)
	fanPartDirection
	fanPartPreset
)

// fanSheet is the sheet while it is up: the fan, and what was set from here ahead of Home Assistant
// saying so.
type fanSheet struct {
	entity string
	// on, speed, direction and preset are what was last set from here; the set... fields say which
	// of them were.
	on                         bool
	speed                      float64
	direction, preset          string
	setOn, setSpeed, setPreset bool
}

// fanView is the sheet as one frame draws it: what the fan is doing now, with what was set from here
// over it.
type fanView struct {
	now dashboard.FanNow
}

// fanWords are the sheet's words, in the screen's language: on and off, the speed, the two
// directions, Done, and the preset modes Home Assistant's fans most often name. Empty is English.
var fanWords = map[string]map[string]string{
	"": {"on": "On", "off": "Off", "speed": "Speed", "forward": "Forward", "reverse": "Reverse", "done": "Done",
		"auto": "Auto", "sleep": "Sleep", "turbo": "Turbo", "low": "Low", "medium": "Medium", "high": "High",
		"normal": "Normal", "nature": "Nature", "eco": "Eco", "smart": "Smart", "manual": "Manual"},
	"de": {"on": "An", "off": "Aus", "speed": "Stufe", "forward": "Vorwärts", "reverse": "Rückwärts", "done": "Fertig",
		"auto": "Auto", "sleep": "Schlaf", "turbo": "Turbo", "low": "Niedrig", "medium": "Mittel", "high": "Hoch",
		"normal": "Normal", "nature": "Natur", "eco": "Eco", "smart": "Smart", "manual": "Manuell"},
	"es": {"on": "Encendido", "off": "Apagado", "speed": "Velocidad", "forward": "Adelante", "reverse": "Invertido", "done": "Listo",
		"auto": "Auto", "sleep": "Sueño", "turbo": "Turbo", "low": "Baja", "medium": "Media", "high": "Alta",
		"normal": "Normal", "nature": "Natural", "eco": "Eco", "smart": "Smart", "manual": "Manual"},
	"fr": {"on": "Marche", "off": "Arrêt", "speed": "Vitesse", "forward": "Avant", "reverse": "Arrière", "done": "OK",
		"auto": "Auto", "sleep": "Nuit", "turbo": "Turbo", "low": "Faible", "medium": "Moyenne", "high": "Forte",
		"normal": "Normal", "nature": "Nature", "eco": "Eco", "smart": "Smart", "manual": "Manuel"},
	"it": {"on": "Acceso", "off": "Spento", "speed": "Velocità", "forward": "Avanti", "reverse": "Indietro", "done": "Fatto",
		"auto": "Auto", "sleep": "Notte", "turbo": "Turbo", "low": "Bassa", "medium": "Media", "high": "Alta",
		"normal": "Normale", "nature": "Natura", "eco": "Eco", "smart": "Smart", "manual": "Manuale"},
	"pt": {"on": "Ligado", "off": "Desligado", "speed": "Velocidade", "forward": "Frente", "reverse": "Reverso", "done": "Pronto",
		"auto": "Auto", "sleep": "Sono", "turbo": "Turbo", "low": "Baixa", "medium": "Média", "high": "Alta",
		"normal": "Normal", "nature": "Natureza", "eco": "Eco", "smart": "Smart", "manual": "Manual"},
	"ru": {"on": "Вкл.", "off": "Выкл.", "speed": "Скорость", "forward": "Вперёд", "reverse": "Назад", "done": "Готово",
		"auto": "Авто", "sleep": "Сон", "turbo": "Турбо", "low": "Низкая", "medium": "Средняя", "high": "Высокая",
		"normal": "Обычный", "nature": "Природа", "eco": "Эко", "smart": "Умный", "manual": "Ручной"},
}

// fanWord is one of the sheet's words; a preset mode it has no word for is shown as Home Assistant
// names it, its first letter capital and underscores as spaces.
func fanWord(key string) string {
	w, ok := fanWords[screenLang()]
	if !ok {
		w = fanWords[""]
	}
	if s, ok := w[key]; ok {
		return s
	}
	if s, ok := fanWords[""][key]; ok {
		return s
	}
	key = strings.ReplaceAll(key, "_", " ")
	if key == "" {
		return key
	}
	return strings.ToUpper(key[:1]) + key[1:]
}

// fanNow and fanCall are how the sheet reads the fan and how what it sets reaches Home Assistant, by
// the dashboard whose sheet it is (sheets); variables so that a test can see what they ask for.
var (
	fanNow  = func(entity string) (dashboard.FanNow, bool) { return sheets().FanNow(entity) }
	fanCall = func(a dashboard.Action) { sheets().Tap(a) }
)

// fanOwnTap is whether a tile's tap is the fan's plain toggle (or none): a card that sets a tap of
// its own keeps what it says, and a long press on it is that tap.
func fanOwnTap(t *dashTile) bool {
	return t.action == nil || (t.action.Service == "fan.toggle" && t.action.Entity == t.entity && t.action.View == "")
}

// openFan puts the fan sheet up for entity.
func (d *Display) openFan(entity string) bool {
	if _, ok := fanNow(entity); !ok {
		return false
	}
	slog.Info("dashboard fan sheet", "entity", entity)
	d.mu.Lock()
	d.dashFan = &fanSheet{entity: entity}
	d.mu.Unlock()
	d.wake()
	return true
}

// fanViewNow is the sheet with what its fan is doing now, for a frame; nil without a sheet.
func (d *Display) fanViewNow() *fanView {
	d.mu.Lock()
	f := d.dashFan
	d.mu.Unlock()
	if f == nil {
		return nil
	}
	now, _ := fanNow(f.entity)
	return &fanView{now: f.over(now)}
}

// over is now with what was set from here put over it.
func (f fanSheet) over(now dashboard.FanNow) dashboard.FanNow {
	if f.setOn {
		now.On = f.on
		if !f.on {
			now.Percentage = 0
		}
	}
	if f.setSpeed {
		now.Percentage, now.On = f.speed, f.speed > 0
	}
	if f.direction != "" {
		now.Direction = f.direction
	}
	if f.setPreset {
		now.Preset = f.preset
	}
	return now
}

// editFan changes the sheet that is up, if it is still the same fan's, and puts it back as a copy.
func (d *Display) editFan(entity string, change func(*fanSheet)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dashFan == nil || d.dashFan.entity != entity {
		return
	}
	n := *d.dashFan
	change(&n)
	d.dashFan = &n
}

// fanTap is a finger lifted at x, y while the fan sheet is up, which the sheet always takes. It says
// whether the sheet was up. (The speed slider is the slider's: slideSheet.)
func (d *Display) fanTap(x, y int) bool {
	d.mu.Lock()
	f := d.dashFan
	d.mu.Unlock()
	if f == nil || d.r == nil {
		return false
	}
	now, _ := fanNow(f.entity)
	now = f.over(now)
	z, in := d.r.fanAt(x, y)
	call := func(service string, data map[string]any) {
		slog.Info("dashboard fan", "entity", f.entity, "service", service)
		fanCall(dashboard.Action{Entity: f.entity, Service: service, Data: data})
	}
	switch {
	case !in || z.kind == fanPartDone:
		d.mu.Lock()
		d.dashFan = nil
		d.mu.Unlock()
	case z.kind == fanPartPower:
		if now.On {
			call("fan.turn_off", nil)
		} else {
			call("fan.turn_on", nil)
		}
		on := !now.On
		d.editFan(f.entity, func(n *fanSheet) { n.on, n.setOn, n.setSpeed = on, true, false })
	case z.kind == fanPartDirection && z.index < len(fanDirections):
		dir := fanDirections[z.index]
		call("fan.set_direction", map[string]any{"direction": dir})
		d.editFan(f.entity, func(n *fanSheet) { n.direction = dir })
	case z.kind == fanPartPreset && z.index < len(now.Presets):
		preset := now.Presets[z.index]
		call("fan.set_preset_mode", map[string]any{"preset_mode": preset})
		d.editFan(f.entity, func(n *fanSheet) { n.preset, n.setPreset, n.setOn, n.on = preset, true, true, true })
	}
	d.wake()
	return true
}

// fanDirections are the two ways a fan turns, in the order the sheet shows them.
var fanDirections = []string{"forward", "reverse"}

// fanSheet draws the fan sheet over the drawn dashboard and keeps where its parts are for fanAt.
func (r *renderer) fanSheet(v *fanView, th dashboard.Theme) {
	if v == nil {
		r.setFan(nil, image.Rectangle{})
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
	h := pad + titleH + subH + rowH + gap + btnH + pad
	if c.HasSpeed {
		h += rowH + gap
	}
	if c.HasDirection {
		h += chipH + gap
	}
	h += ((len(c.Presets) + cols - 1) / cols) * (chipH + gap)
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
	line := fanWord("off")
	if c.On {
		line = fanWord("on")
		if c.HasSpeed && c.Percentage > 0 {
			line += " · " + fmt.Sprintf("%d%%", int(c.Percentage+0.5))
		}
		if c.Preset != "" {
			line += " · " + fanWord(c.Preset)
		}
	}
	r.text(fc.sub, r.fit(fc.sub, line, inner), x0+pad, y+r.s(20), pal.sub)
	y += subH

	// On or off: the one switch, lit while the fan runs.
	{
		b := image.Rect(x0+pad, y, x0+w-pad, y+rowH)
		fill, fg := lerp(pal.card, pal.text, 0.12), pal.text
		if c.On {
			fill, fg = pal.accent, pal.bg
		}
		r.roundFill(b, float64(rowH)/2, fill, fill)
		word := fanWord("off")
		if c.On {
			word = fanWord("on")
		}
		is := 32
		tw := r.s(is) + r.s(12) + r.width(fc.button, word)
		tx := b.Min.X + (b.Dx()-tw)/2
		r.mdiIcon("power", tx, b.Min.Y+(rowH-r.s(is))/2, is, fg)
		r.text(fc.button, word, tx+r.s(is)+r.s(12), b.Min.Y+rowH/2+r.s(9), fg)
		zones = append(zones, mediaZone{r: b, kind: fanPartPower})
		y += rowH + gap
	}

	// The speed: a bar the whole height of its row, as the media sheet's volume is.
	if c.HasSpeed {
		is := 32
		r.mdiIcon("fan", x0+pad, y+(rowH-r.s(is))/2, is, pal.sub)
		bx := x0 + pad + r.s(is) + gap
		label := fanWord("speed") + " " + fmt.Sprintf("%d%%", int(c.Percentage+0.5))
		lw := r.width(fc.label, label)
		bar := image.Rect(bx, y+rowH/2-r.s(7), x0+w-pad-lw-gap, y+rowH/2+r.s(7))
		track := lerp(pal.card, pal.text, 0.15)
		r.roundFill(bar, float64(bar.Dy())/2, track, track)
		if fw := int(float64(bar.Dx())*min(max(c.Percentage/100, 0), 1) + 0.5); fw > 0 {
			r.roundFill(image.Rect(bar.Min.X, bar.Min.Y, bar.Min.X+fw, bar.Max.Y), float64(bar.Dy())/2, pal.accent, pal.accent)
		}
		r.text(fc.label, label, x0+w-pad-lw, y+rowH/2+r.s(9), pal.text)
		zones = append(zones, mediaZone{r: image.Rect(bar.Min.X, y, bar.Max.X, y+rowH), kind: fanPartSpeed})
		y += rowH + gap
	}

	// A chip, lit when it is the one the fan is in.
	chip := func(b image.Rectangle, word string, lit bool) {
		fill, fg := lerp(pal.card, pal.text, 0.1), pal.text
		if lit {
			fill, fg = pal.accent, pal.bg
		}
		r.roundFill(b, pal.rad*0.6, fill, fill)
		word = r.fit(fc.sub, word, b.Dx()-r.s(16))
		r.text(fc.sub, word, b.Min.X+(b.Dx()-r.width(fc.sub, word))/2, b.Min.Y+chipH/2+r.s(7), fg)
	}
	if c.HasDirection {
		half := (inner - gap) / 2
		for i, dir := range fanDirections {
			cx := x0 + pad + i*(half+gap)
			b := image.Rect(cx, y, cx+half, y+chipH)
			chip(b, fanWord(dir), c.Direction == dir)
			zones = append(zones, mediaZone{r: b, kind: fanPartDirection, index: i})
		}
		y += chipH + gap
	}
	for i, m := range c.Presets {
		cx := x0 + pad + (i%cols)*(chipW+gap)
		cy := y + (i/cols)*(chipH+gap)
		b := image.Rect(cx, cy, cx+chipW, cy+chipH)
		chip(b, fanWord(m), c.On && m == c.Preset)
		zones = append(zones, mediaZone{r: b, kind: fanPartPreset, index: i})
	}

	done := fanWord("done")
	bw := max(r.width(fc.button, done)+2*r.s(28), r.s(140))
	btn := image.Rect(x0+w-pad-bw, y0+h-pad-btnH, x0+w-pad, y0+h-pad)
	r.roundFill(btn, pal.rad, pal.accent, pal.accent)
	r.text(fc.button, done, btn.Min.X+(bw-r.width(fc.button, done))/2, btn.Min.Y+btnH/2+r.s(9), pal.bg)
	zones = append(zones, mediaZone{r: btn, kind: fanPartDone})
	r.setFan(zones, card)
}

// fanParts is the fan sheet's parts and the sheet itself where they were last drawn, under zmu:
// empty while no sheet is up.
type fanParts struct {
	fanZones []mediaZone
	fanCard  image.Rectangle
}

// setFan keeps the fan sheet's parts where they were drawn, for the touch goroutine.
func (r *renderer) setFan(zones []mediaZone, card image.Rectangle) {
	r.zmu.Lock()
	r.fanZones, r.fanCard = zones, card
	r.zmu.Unlock()
}

// fanAt is the part of the fan sheet under x, y, and whether that is on the sheet at all.
func (r *renderer) fanAt(x, y int) (mediaZone, bool) {
	r.zmu.Lock()
	zones, card := slices.Clone(r.fanZones), r.fanCard
	r.zmu.Unlock()
	z, _, in := mediaHit(zones, card, x, y)
	return z, in
}
