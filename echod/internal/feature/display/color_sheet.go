//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"log/slog"
	"math"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// The color sheet: a finger resting on a light's tile on the drawn dashboard, and lifting without
// sliding or scrolling, brings up the light's whites and colors over the page. A tap on the band of
// whites, or a finger lifted on it after moving along it, sets that white; a tap on a color sets the
// color. Done, or a tap beside the sheet, puts it away.

// What a part of the sheet is, for a tap.
const (
	colorPartNone = iota
	colorPartDone
	colorPartWhite
	colorPartHue
)

// colorHues are the colors the sheet offers, around the wheel: red, orange, yellow, green, cyan,
// blue, violet, pink.
var colorHues = []float64{0, 28, 52, 120, 180, 225, 275, 320}

// kelvinSnap is the step whites are set in.
const kelvinSnap = 50

// colorWords are the sheet's words, in the screen's language: the warm end of the whites, the cool
// end, Done, and what a group of lights is told about its colors. Empty is English.
var colorWords = map[string][4]string{
	"":   {"warm", "cool", "Done", "Group · lights without color stay white"},
	"de": {"warm", "kalt", "Fertig", "Gruppe · Lampen ohne Farbe bleiben weiß"},
	"es": {"cálido", "frío", "Listo", "Grupo · las luces sin color quedan blancas"},
	"fr": {"chaud", "froid", "OK", "Groupe · les lampes sans couleur restent blanches"},
	"it": {"caldo", "freddo", "Fatto", "Gruppo · le luci senza colore restano bianche"},
}

func colorWord(i int) string {
	w, ok := colorWords[screenLang()]
	if !ok {
		w = colorWords[""]
	}
	return w[i]
}

// longPress is a finger that came down on a tile and lifted without moving, held long enough to be a
// hold. On a light with whites or colors it opens the color sheet. Anything else does what a tap does,
// as such a press did before the dashboard asked for holds.
func (d *Display) longPress(t *dashTile) {
	if t == nil {
		return
	}
	if t.adjust != nil && t.adjust.Kind == "brightness" {
		if c, ok := dashboard.Get().LightColor(t.adjust.Entity); ok {
			slog.Info("dashboard color sheet", "entity", c.Entity, "whites", c.Kelvin, "colors", c.Colors)
			d.mu.Lock()
			d.dashColor = &c
			d.mu.Unlock()
			d.wake()
			return
		}
	}
	if t.action != nil {
		dashboard.Get().Tap(*t.action)
	}
}

// colorOpen is whether the color sheet is up.
func (d *Display) colorOpen() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dashColor != nil
}

// onColorSheet is whether x, y is on the color sheet, while it is up.
func (d *Display) onColorSheet(x, y int) bool {
	if !d.colorOpen() || d.r == nil {
		return false
	}
	_, in := d.r.colorAt(x, y)
	return in
}

// sliderAt is the slider of the sheet that is up under x, y: the color sheet's whites.
func (d *Display) sliderAt(x, y int) (sheetSlider, bool) {
	if d.r == nil {
		return sheetSlider{}, false
	}
	if z, in := d.r.colorAt(x, y); in && z.kind == colorPartWhite {
		return sheetSlider{kind: colorPartWhite, r: z.r, lo: z.lo, hi: z.hi}, true
	}
	return sheetSlider{}, false
}

// slideSheet moves a sheet's slider to the finger at x, as a tile's level follows a finger sliding
// along it; final, when the finger lifts, sets what it shows.
func (d *Display) slideSheet(s sheetSlider, x int, final bool) {
	v := s.at(x)
	switch s.kind {
	case colorPartWhite:
		k := math.Round(v/kelvinSnap) * kelvinSnap
		k = min(max(k, s.lo), s.hi)
		d.setShownColor(k)
		d.mu.Lock()
		c := d.dashColor
		d.mu.Unlock()
		if final && c != nil {
			slog.Info("dashboard color", "entity", c.Entity, "kelvin", k)
			dashboard.Get().Tap(dashboard.Action{Entity: c.Entity, Service: "light.turn_on",
				Data: map[string]any{"color_temp_kelvin": int(k)}})
		}
	}
	d.wake()
}

// colorTap is a finger lifted at x, y while the color sheet is up, which the sheet always takes: the
// page under it is not tapped. It says whether the sheet was up.
func (d *Display) colorTap(x, y int) bool {
	d.mu.Lock()
	c := d.dashColor
	d.mu.Unlock()
	if c == nil || d.r == nil {
		return false
	}
	z, in := d.r.colorAt(x, y)
	switch {
	case !in || z.kind == colorPartDone:
		d.mu.Lock()
		d.dashColor = nil
		d.mu.Unlock()
	case z.kind == colorPartWhite:
		slog.Info("dashboard color", "entity", c.Entity, "kelvin", z.value)
		dashboard.Get().Tap(dashboard.Action{Entity: c.Entity, Service: "light.turn_on",
			Data: map[string]any{"color_temp_kelvin": int(z.value)}})
		d.setShownColor(z.value)
	case z.kind == colorPartHue:
		slog.Info("dashboard color", "entity", c.Entity, "hue", z.value)
		dashboard.Get().Tap(dashboard.Action{Entity: c.Entity, Service: "light.turn_on",
			Data: map[string]any{"hs_color": []any{z.value, 100.0}}})
		d.setShownColor(0)
	}
	d.wake()
	return true
}

// setShownColor moves the sheet's mark to the white just chosen, or takes it off for a color, without
// waiting for Home Assistant to say so.
func (d *Display) setShownColor(kelvin float64) {
	d.mu.Lock()
	if d.dashColor != nil {
		n := *d.dashColor
		n.NowK = kelvin
		d.dashColor = &n
	}
	d.mu.Unlock()
}

// colorSheet draws the color sheet over the drawn dashboard and keeps where its parts are for
// colorAt. Without a sheet there is nothing of it to tap.
func (r *renderer) colorSheet(c *dashboard.LightColor, th dashboard.Theme) {
	if c == nil {
		r.setColor(nil, image.Rectangle{})
		return
	}
	pal := r.palette(th)
	fc := r.faces()
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(color.RGBA{0, 0, 0, 150}), image.Point{}, draw.Over)

	pad, gap := r.s(20), r.s(14)
	bandH, swatchH, btnH, wordsH, noteH := r.s(64), r.s(60), r.s(52), r.s(30), r.s(30)
	w := min(r.w-2*r.s(40), r.s(640))
	h := pad + r.s(cardTitleH)
	// A group's colors reach only the lights in it that have colors: it says so under its name.
	note := c.Group && c.Colors
	if note {
		h += noteH
	}
	if c.Kelvin {
		h += bandH + wordsH + gap
	}
	if c.Colors {
		h += swatchH + gap
	}
	h += btnH + pad
	x0, y0 := (r.w-w)/2, max((r.h-h)/2, r.s(8))
	card := image.Rect(x0, y0, x0+w, y0+h)
	r.roundFill(card, pal.rad, pal.card, pal.card)
	inner := w - 2*pad

	var zones []colorZone
	y := y0 + pad
	r.text(fc.labelBold, r.fit(fc.labelBold, c.Name, inner), x0+pad, y+r.s(28), pal.text)
	y += r.s(cardTitleH)
	if note {
		r.text(fc.sub, r.fit(fc.sub, colorWord(3), inner), x0+pad, y+r.s(16), pal.sub)
		y += noteH
	}

	if c.Kelvin {
		band := image.Rect(x0+pad, y, x0+pad+inner, y+bandH)
		span := float64(max(band.Dx()-1, 1))
		for x := band.Min.X; x < band.Max.X; x++ {
			k := c.MinK + float64(x-band.Min.X)/span*(c.MaxK-c.MinK)
			draw.Draw(r.dst, image.Rect(x, band.Min.Y, x+1, band.Max.Y), image.NewUniform(kelvinRGB(k)), image.Point{}, draw.Src)
		}
		if c.NowK > 0 {
			mx := band.Min.X + int((c.NowK-c.MinK)/(c.MaxK-c.MinK)*span+0.5)
			mx = min(max(mx, band.Min.X+r.s(3)), band.Max.X-r.s(3))
			mark := image.Rect(mx-r.s(3), band.Min.Y-r.s(5), mx+r.s(3), band.Max.Y+r.s(5))
			draw.Draw(r.dst, mark, image.NewUniform(pal.text), image.Point{}, draw.Src)
		}
		zones = append(zones, colorZone{r: band, kind: colorPartWhite, lo: c.MinK, hi: c.MaxK})
		y += bandH
		warm, cool := colorWord(0), colorWord(1)
		r.text(fc.sub, warm, band.Min.X, y+r.s(24), pal.sub)
		r.text(fc.sub, cool, band.Max.X-r.width(fc.sub, cool), y+r.s(24), pal.sub)
		y += wordsH + gap
	}

	if c.Colors {
		n := len(colorHues)
		sg := r.s(10)
		sw := (inner - sg*(n-1)) / n
		for i, hue := range colorHues {
			x := x0 + pad + i*(sw+sg)
			b := image.Rect(x, y, x+sw, y+swatchH)
			col := hueRGB(hue)
			r.roundFill(b, pal.rad*0.6, col, col)
			zones = append(zones, colorZone{r: b, kind: colorPartHue, value: hue})
		}
		y += swatchH + gap
	}

	done := colorWord(2)
	bw := max(r.width(fc.button, done)+2*r.s(28), r.s(140))
	btn := image.Rect(x0+w-pad-bw, y, x0+w-pad, y+btnH)
	r.roundFill(btn, pal.rad, pal.accent, pal.accent)
	r.text(fc.button, done, btn.Min.X+(bw-r.width(fc.button, done))/2, btn.Min.Y+btnH/2+r.s(9), pal.bg)
	zones = append(zones, colorZone{r: btn, kind: colorPartDone})
	r.setColor(zones, card)
}

// setColor keeps the color sheet's parts where they were drawn, for the touch goroutine.
func (p *paint) setColor(zones []colorZone, card image.Rectangle) {
	p.zmu.Lock()
	p.colorZones, p.colorCard = zones, card
	p.zmu.Unlock()
}

// colorAt is the part of the color sheet under x, y, and whether that is on the sheet at all.
func (p *paint) colorAt(x, y int) (colorZone, bool) {
	p.zmu.Lock()
	zones, card := p.colorZones, p.colorCard
	p.zmu.Unlock()
	return colorHit(zones, card, x, y)
}

// colorHit is the part of a sheet drawn as zones and card under x, y. On the band of whites it carries
// the white under the finger, on kelvinSnap's steps.
func colorHit(zones []colorZone, card image.Rectangle, x, y int) (colorZone, bool) {
	pt := image.Pt(x, y)
	if !pt.In(card) {
		return colorZone{}, false
	}
	for _, z := range zones {
		if !pt.In(z.r) {
			continue
		}
		if z.kind == colorPartWhite {
			f := float64(x-z.r.Min.X) / float64(max(z.r.Dx()-1, 1))
			k := math.Round((z.lo+f*(z.hi-z.lo))/kelvinSnap) * kelvinSnap
			z.value = min(max(k, z.lo), z.hi)
		}
		return z, true
	}
	return colorZone{kind: colorPartNone}, true
}

// kelvinRGB is roughly the color of a white at k kelvin, after Tanner Helland's fit of the black-body
// curve: orange at the warm end, blue-white at the cool one.
func kelvinRGB(k float64) color.RGBA {
	t := k / 100
	var r, g, b float64
	if t <= 66 {
		r = 255
		g = 99.4708025861*math.Log(t) - 161.1195681661
	} else {
		r = 329.698727446 * math.Pow(t-60, -0.1332047592)
		g = 288.1221695283 * math.Pow(t-60, -0.0755148492)
	}
	switch {
	case t >= 66:
		b = 255
	case t <= 19:
		b = 0
	default:
		b = 138.5177312231*math.Log(t-10) - 305.0447927307
	}
	return color.RGBA{byte8(r), byte8(g), byte8(b), 255}
}

// hueRGB is a hue at full saturation and brightness.
func hueRGB(h float64) color.RGBA {
	h = math.Mod(h, 360) / 60
	x := 1 - math.Abs(math.Mod(h, 2)-1)
	var r, g, b float64
	switch int(h) {
	case 0:
		r, g = 1, x
	case 1:
		r, g = x, 1
	case 2:
		g, b = 1, x
	case 3:
		g, b = x, 1
	case 4:
		r, b = x, 1
	default:
		r, b = 1, x
	}
	return color.RGBA{byte8(r * 255), byte8(g * 255), byte8(b * 255), 255}
}

func byte8(v float64) uint8 { return uint8(min(max(math.Round(v), 0), 255)) }
