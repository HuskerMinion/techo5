//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"log/slog"
	"math"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
)

// The color sheet: a finger resting on a light's tile on the drawn dashboard, and lifting without
// sliding or scrolling, brings up the light's whites and colors over the page: a band of whites from
// warm to cool, and a band of colors around the wheel, with marks at the colors most often wanted. A
// tap on a band sets what is under it; a finger sliding along one takes the light along with it.
// Done, or a tap beside the sheet, puts it away.

// What a part of the sheet is, for a tap.
const (
	colorPartNone = iota
	colorPartDone
	colorPartWhite
	colorPartHue
)

// colorHues are the colors marked under the band of colors, around the wheel: red, orange, yellow,
// green, cyan, blue, violet, pink.
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

// sliderAt is the slider of the color sheet under x, y: its band of whites or its band of colors.
func (d *Display) sliderAt(x, y int) (sheetSlider, bool) {
	if d.r == nil {
		return sheetSlider{}, false
	}
	if z, in := d.r.colorAt(x, y); in && (z.kind == colorPartWhite || z.kind == colorPartHue) {
		return sheetSlider{kind: z.kind, r: z.r, lo: z.lo, hi: z.hi}, true
	}
	return sheetSlider{}, false
}

// slideSheet moves a sheet's slider to the finger at x, as a tile's level follows a finger sliding
// along it, and the light follows it too (sendSlider); final, when the finger lifts, sets where it
// ended.
func (d *Display) slideSheet(s sheetSlider, x int, final bool) {
	v := s.at(x)
	switch s.kind {
	case colorPartWhite:
		v = min(max(math.Round(v/kelvinSnap)*kelvinSnap, s.lo), s.hi)
		d.setShownColor(v)
	case colorPartHue:
		v = hueAt(v / 360)
		d.setShownHue(v)
	}
	d.sendSlider(s, v, final)
	d.wake()
}

// sheetSendEvery is how often a sheet's slider sends what it shows while a finger moves it: the light
// warms or cools or changes color as the finger goes, a few steps a second rather than one for every
// movement, which a Zigbee light would queue up behind.
const sheetSendEvery = 300 * time.Millisecond

// sliderTap is how a slider's value reaches Home Assistant; a variable so that a test can see what
// is sent, and when.
var sliderTap = func(a dashboard.Action) { dashboard.Get().Tap(a) }

// sliderSent is what a sheet's slider last sent, and the value waiting to be sent while sends are held
// back. gen is which wait a held-back send belongs to: a send, or the finger lifting, starts the next,
// and a timer of an earlier one that fires anyway sends nothing.
type sliderSent struct {
	at      time.Time
	value   float64
	sent    bool
	pending float64
	timer   *time.Timer
	gen     uint64
}

// sliderAfter starts the timer for a held-back send; a variable so that a test can fire one late, as
// the race it guards against does.
var sliderAfter = time.AfterFunc

// sendSlider sends a slider's value to its light: at once when the last send was long
// enough ago, else once it is (the last value a finger stopped on is sent too), and always when the
// finger lifts - each value once.
func (d *Display) sendSlider(s sheetSlider, value float64, final bool) {
	kind := s.kind
	d.mu.Lock()
	st := &d.sheetSent
	st.pending = value
	if wait := sheetSendEvery - time.Since(st.at); !final && wait > 0 {
		if st.timer == nil {
			gen := st.gen
			st.timer = sliderAfter(wait, func() {
				d.mu.Lock()
				if d.sheetSent.gen != gen {
					// The finger lifted, or a send went first, after this timer could no longer be
					// stopped: what it would send is not what the slider shows.
					d.mu.Unlock()
					return
				}
				d.sheetSent.timer = nil
				v := d.sheetSent.pending
				d.mu.Unlock()
				d.sendSlider(s, v, false)
			})
		}
		d.mu.Unlock()
		return
	}
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
	st.gen++ // a timer that fires anyway is too late
	repeat := st.sent && st.value == value
	st.at, st.value, st.sent = time.Now(), value, true
	if final {
		*st = sliderSent{gen: st.gen} // the next finger starts afresh
	}
	c := d.dashColor
	d.mu.Unlock()
	if repeat {
		return
	}
	log := slog.Debug
	if final {
		log = slog.Info
	}
	// The steps on the way fade over as long as they come apart, so the light moves from one to the
	// next as the finger does rather than jumping, and is there as the next comes: the light's own fade
	// (half a second on a Hue bulb) put each step behind the one before. Where the finger lifts fades
	// in as the light always does.
	light := func(data map[string]any) map[string]any {
		if !final {
			data["transition"] = sheetSendEvery.Seconds()
		}
		return data
	}
	switch {
	case kind == colorPartWhite && c != nil:
		log("dashboard color", "entity", c.Entity, "kelvin", value)
		sliderTap(dashboard.Action{Entity: c.Entity, Service: "light.turn_on",
			Data: light(map[string]any{"color_temp_kelvin": int(value)})})
	case kind == colorPartHue && c != nil:
		log("dashboard color", "entity", c.Entity, "hue", value)
		sliderTap(dashboard.Action{Entity: c.Entity, Service: "light.turn_on",
			Data: light(map[string]any{"hs_color": []any{value, 100.0}})})
	}
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
		d.setShownHue(z.value)
	}
	d.wake()
	return true
}

// setShownColor moves the sheet's mark to the white just chosen, and off the colors, without waiting
// for Home Assistant to say so.
func (d *Display) setShownColor(kelvin float64) {
	d.mu.Lock()
	if d.dashColor != nil {
		n := *d.dashColor
		n.NowK = kelvin
		n.HasHue = n.HasHue && kelvin <= 0
		d.dashColor = &n
	}
	d.mu.Unlock()
}

// setShownHue moves the sheet's mark to the color just chosen, and off the whites.
func (d *Display) setShownHue(hue float64) {
	d.mu.Lock()
	if d.dashColor != nil {
		n := *d.dashColor
		n.NowK, n.HasHue, n.NowHue = 0, true, hue
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
	tickH := r.s(14)
	if c.Colors {
		h += swatchH + tickH + gap
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
		// The band of colors, the whole wheel along it, with a small mark under each of the colors
		// most often wanted, and the light's own color marked on it when it is on one.
		band := image.Rect(x0+pad, y, x0+pad+inner, y+swatchH)
		span := float64(max(band.Dx()-1, 1))
		for x := band.Min.X; x < band.Max.X; x++ {
			hue := float64(x-band.Min.X) / span * 360
			draw.Draw(r.dst, image.Rect(x, band.Min.Y, x+1, band.Max.Y), image.NewUniform(hueRGB(hue)), image.Point{}, draw.Src)
		}
		for _, hue := range colorHues {
			tx := band.Min.X + int(hue/360*span+0.5)
			for i := 0; i < r.s(8); i++ {
				row := image.Rect(tx-i*3/4, band.Max.Y+r.s(3)+i, tx+i*3/4+1, band.Max.Y+r.s(4)+i)
				draw.Draw(r.dst, row, image.NewUniform(pal.sub), image.Point{}, draw.Src)
			}
		}
		if c.HasHue {
			mx := band.Min.X + int(c.NowHue/360*span+0.5)
			mx = min(max(mx, band.Min.X+r.s(3)), band.Max.X-r.s(3))
			mark := image.Rect(mx-r.s(3), band.Min.Y-r.s(5), mx+r.s(3), band.Max.Y+r.s(5))
			draw.Draw(r.dst, mark, image.NewUniform(pal.text), image.Point{}, draw.Src)
		}
		zones = append(zones, colorZone{r: band, kind: colorPartHue, lo: 0, hi: 360})
		y += swatchH + tickH + gap
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
		f := float64(x-z.r.Min.X) / float64(max(z.r.Dx()-1, 1))
		switch z.kind {
		case colorPartWhite:
			k := math.Round((z.lo+f*(z.hi-z.lo))/kelvinSnap) * kelvinSnap
			z.value = min(max(k, z.lo), z.hi)
		case colorPartHue:
			z.value = hueAt(f)
		}
		return z, true
	}
	return colorZone{kind: colorPartNone}, true
}

// hueAt is the hue a fraction of the way along the band of colors, in whole degrees; its right end is
// red again, which is 0.
func hueAt(f float64) float64 {
	return math.Mod(math.Round(min(max(f, 0), 1)*360), 360)
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
