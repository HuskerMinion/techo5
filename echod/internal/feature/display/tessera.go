//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/dashboard"
	"github.com/HuskerMinion/techo5/echod/internal/feature/tessera"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// The Tessera page: the layout the Tessera add-on sent (feature/tessera), drawn as the dashboard page
// when the Dashboard setting is Tessera. It is reached as any dashboard is - a swipe in from the left,
// dashboard_show, or in place of the clock when idle - so the screen's own edges, the idle return and
// "go home" keep working on it.
//
// A page is a top bar (its title, its items, the time), the grid of tiles, and a dot for each page along
// the foot. The tiles are the drawn dashboard's, in the device's own colors: a round badge with the
// icon, lit when the thing is on, the name, and what it is doing. A tap does what the tile is for, a
// finger sliding along a tile with a level sets it, and a swipe across the page goes to the next one.
// A long press opens the drawn dashboard's sheets - a light's colors, a thermostat's controls, a
// player's music - fed from the tiles' own states, which the add-on keeps up to date, and sending what
// is set as the device's own actions, as a tap does (tessSheets). A tap on a locked lock asks for a
// second before it unlocks.

// Sizes, in the Show 5's pixels as the rest of the package's are.
const (
	tessBarH  = 64 // the top bar
	tessDotsH = 30 // the strip of page dots
	tessDot   = 10 // a dot's size
)

// tessSwipe is how far a finger goes across the page to turn it.
const tessSwipe = 80

// swatches are the backgrounds a layout may give a tile, as tints the card takes on.
var swatches = map[string]color.RGBA{
	"red":    {0xe5, 0x6b, 0x6f, 255},
	"orange": {0xf0, 0x9a, 0x50, 255},
	"yellow": {0xe8, 0xc5, 0x4a, 255},
	"green":  {0x6a, 0xb8, 0x6e, 255},
	"mint":   {0x5f, 0xc4, 0xa8, 255},
	"blue":   {0x5b, 0x9b, 0xe0, 255},
	"purple": {0x9c, 0x7c, 0xd8, 255},
	"pink":   {0xe0, 0x7c, 0xb4, 255},
	"gray":   {0x9a, 0x9a, 0x9a, 255},
}

// tessScene fetches the layout for the page, and keeps the page shown on one that has it.
func (d *Display) tessScene(s *scene) {
	v, on := tessera.Current()
	d.mu.Lock()
	defer d.mu.Unlock()
	// The first layout opens on its home page, and so does one that no longer has the page that was up.
	if v.Rev != d.tessRev && (d.tessRev == "" || d.tessPage >= len(v.Pages)) {
		d.tessPage = v.Home
	}
	d.tessRev = v.Rev
	if d.tessPage >= len(v.Pages) || d.tessPage < 0 {
		d.tessPage = max(min(v.Home, len(v.Pages)-1), 0)
	}
	s.tessera, s.tessOn, s.tessPage, s.dashAdjust, s.dashColor = v, on, d.tessPage, d.dashAdjust, d.dashColor
}

// tessSheets is the Tessera page's side of the sheets: what an entity is doing, from the tile that
// shows it, as the add-on last sent it, and what is set sent as the device's own action, as a tap on a
// tile is. The drawn dashboard's own connection to Home Assistant is not there to use: it is opened
// for the drawn page alone, with a token the Tessera page does not need.
type tessSheets struct{}

// sheetSource is where a sheet reads what its entity is doing and sends what is set: the drawn
// dashboard, or the Tessera page.
type sheetSource interface {
	ClimateNow(entity string) (dashboard.ClimateNow, bool)
	LightColor(entity string) (dashboard.LightColor, bool)
	MediaNow(entity string) (dashboard.MediaNow, bool)
	Tap(a dashboard.Action)
	SetClimate(entity string, low, high float64)
	SetHVACMode(entity, mode string)
}

// sheets is the source of the dashboard shown: the sheets go with the page they open over.
func sheets() sheetSource {
	if dashboard.Get().Mode() == config.DashboardTessera {
		return tessSheets{}
	}
	return dashboard.Get()
}

func (tessSheets) ClimateNow(entity string) (dashboard.ClimateNow, bool) {
	state, a, ok := tessera.Entity(entity)
	if !ok || tesseraDomain(entity) != "climate" {
		return dashboard.ClimateNow{}, false
	}
	return dashboard.ClimateOf(entity, state, a), true
}

func (tessSheets) LightColor(entity string) (dashboard.LightColor, bool) {
	state, a, ok := tessera.Entity(entity)
	if !ok || tesseraDomain(entity) != "light" {
		return dashboard.LightColor{}, false
	}
	c := dashboard.LightColorOf(entity, state, a)
	return c, c.Some()
}

func (tessSheets) MediaNow(entity string) (dashboard.MediaNow, bool) {
	state, a, ok := tessera.Entity(entity)
	if !ok || tesseraDomain(entity) != "media_player" {
		return dashboard.MediaNow{}, false
	}
	return dashboard.MediaOf(entity, state, a), true
}

// Tap runs a sheet's action. A player's play or pause, next and back go where the drawn dashboard's
// go: to Music Assistant's player of the same name while it plays there, which reaches every speaker
// grouped in, worked out off the finger's goroutine.
func (tessSheets) Tap(a dashboard.Action) {
	if a.Service == "" {
		return
	}
	_, named := a.Data["entity_id"]
	for _, service := range tessKeys {
		if a.Service == service && !named && a.Entity != "" {
			go func() { tessera.Call(tessRoute(a.Entity), a.Service, a.Data) }()
			return
		}
	}
	tessera.Call(a.Entity, a.Service, a.Data)
}

func (tessSheets) SetClimate(entity string, low, high float64) {
	data := map[string]any{"temperature": low}
	if high > 0 {
		data = map[string]any{"target_temp_low": low, "target_temp_high": high}
	}
	tessera.Call(entity, "climate.set_temperature", data)
}

func (tessSheets) SetHVACMode(entity, mode string) {
	tessera.Call(entity, "climate.set_hvac_mode", map[string]any{"hvac_mode": mode})
}

// tessSheet opens the sheet of entity, where it has one: a light's colors, a thermostat's controls, a
// player's music. It says whether one opened.
func (d *Display) tessSheet(entity string) bool {
	switch tesseraDomain(entity) {
	case "media_player":
		return d.openMedia(entity)
	case "climate":
		return d.openClimate(entity)
	case "light":
		c, ok := sheets().LightColor(entity)
		if !ok {
			return false
		}
		slog.Info("tessera color sheet", "entity", entity, "whites", c.Kelvin, "colors", c.Colors)
		d.mu.Lock()
		d.dashColor = &c
		d.mu.Unlock()
		d.wake()
		return true
	}
	return false
}

// tessSheetViews fills in the sheets that are up over the Tessera page, for a frame.
func (d *Display) tessSheetViews(s *scene) {
	s.dashMedia = d.mediaViewNow()
	s.dashClimate = d.climateViewNow()
}

// tesseraPage draws the Tessera page over the whole panel.
func (r *paint) tesseraPage(v tessera.View, on bool, page int, adj dashAdjusting, now time.Time) {
	pal := r.palette(dashboard.Theme{})
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(pal.bg), image.Point{}, draw.Src)
	fc := r.faces()
	if !on || len(v.Pages) == 0 {
		msg := "Waiting for a layout from Tessera. Add tiles for this screen in the Tessera add-on."
		if !on {
			msg = "Turn on Tessera screen in Home Assistant to show the layout the Tessera add-on sends."
		}
		lines := r.wrapLines(fc.label, msg, r.w*2/3)
		y := r.h/2 - len(lines)*r.s(36)/2
		for _, line := range lines {
			y += r.s(36)
			r.text(fc.label, line, (r.w-r.width(fc.label, line))/2, y, pal.sub)
		}
		r.setDash(nil, r.h)
		return
	}
	page = min(max(page, 0), len(v.Pages)-1)
	p := v.Pages[page]
	var zones []dashTile

	// The top bar: a way back on a detail page, the page's title, its items, and the time.
	side := r.s(dashSide)
	bar := image.Rect(0, 0, r.w, r.s(tessBarH))
	base := bar.Min.Y + bar.Dy()/2 + r.s(12)
	x := side
	if p.Excluded {
		back := image.Rect(0, 0, side+r.s(48), bar.Max.Y)
		r.mdiIconIn("chevron-left", image.Rect(side-r.s(6), bar.Min.Y, side+r.s(38), bar.Max.Y), 40, pal.text)
		zones = append(zones, dashTile{r: back, page: v.Home + 1})
		x += r.s(44)
	}
	title := p.Title
	if title == "" {
		title = v.Title
	}
	right := r.w - side
	clock := true
	for _, it := range p.Bar {
		clock = clock && it.Kind != "clock"
	}
	// From the right: the time, then the layout's items backwards, so that they read in its order towards
	// the title, as many as fit in half of the bar.
	var order []tessera.BarItem
	if clock {
		order = append(order, tessera.BarItem{Kind: "clock"})
	}
	for i := len(p.Bar) - 1; i >= 0; i-- {
		order = append(order, p.Bar[i])
	}
	for _, it := range order {
		w := r.barItem(it, right, base, pal, now, false)
		if right-w < r.w/2 {
			break
		}
		r.barItem(it, right, base, pal, now, true)
		right -= w + r.s(22)
	}
	r.text(fc.header, r.fit(fc.header, title, right-x-r.s(16)), x, base+r.s(2), pal.text)

	// The page dots, when there is more than one page to swipe between.
	var shown []int
	for i, pg := range v.Pages {
		if !pg.Excluded {
			shown = append(shown, i)
		}
	}
	foot := r.s(dashSide)
	if len(shown) > 1 && !p.Excluded {
		foot = r.s(tessDotsH)
		r.footControls(r.h - foot)
		dot, gap := r.s(tessDot), r.s(14)
		w := len(shown)*dot + (len(shown)-1)*gap
		dx := (r.w - w) / 2
		cy := r.h - foot/2 - r.s(2)
		for k, i := range shown {
			c := lerp(pal.bg, pal.sub, 0.55)
			if i == page {
				c = pal.accent
			}
			at := image.Rect(dx+k*(dot+gap), cy-dot/2, dx+k*(dot+gap)+dot, cy-dot/2+dot)
			r.roundFill(at, float64(dot)/2, c, c)
			zones = append(zones, dashTile{r: at.Inset(-gap / 2).Union(image.Rect(at.Min.X-gap/2, r.h-foot, at.Max.X+gap/2, r.h)), page: i + 1})
		}
	}

	// The grid: the cells share what is between the bar and the foot.
	gap := r.s(dashGap)
	area := image.Rect(side, bar.Max.Y, r.w-side, r.h-foot)
	cw := (area.Dx() - gap*(v.Columns-1)) / v.Columns
	ch := (area.Dy() - gap*(v.Rows-1)) / v.Rows
	for _, t := range p.Tiles {
		x, y := area.Min.X+t.Col*(cw+gap), area.Min.Y+t.Row*(ch+gap)
		box := image.Rect(x, y, x+t.Cols*cw+(t.Cols-1)*gap, y+t.Rows*ch+(t.Rows-1)*gap)
		// A tile's controls go first, for a finger on one to find it rather than the tile.
		zones = append(zones, r.tessTile(box, t, pal, adj)...)
		z := dashTile{r: box, entity: t.Entity, tess: t.I + 1}
		if l := t.Level; l != nil {
			z.adjust = &dashboard.Adjust{Entity: t.Entity, Kind: l.Kind, Value: l.Value, Min: l.Min, Max: l.Max, Step: l.Step}
		}
		if t.Taps || z.adjust != nil {
			zones = append(zones, z)
		}
	}
	// No scroll: the page is what fits.
	r.setDash(zones, r.h)
}

// barText is a top-bar item's words: the clock, date and age are the device's own, the date in the
// screen language like the clock's.
func barText(it tessera.BarItem, now time.Time) string {
	switch it.Kind {
	case "clock":
		return clockText(now)
	case "date":
		return locale.ShortDate(now, screenLang())
	case "ago":
		return agoText(now.Sub(it.At))
	}
	return it.Text
}

// barItem draws one top-bar item ending at right, its words on base, and says how wide it is; with drawIt
// false it only measures.
func (r *paint) barItem(it tessera.BarItem, right, base int, pal dashPal, now time.Time, drawIt bool) int {
	fc := r.faces()
	text := barText(it, now)
	c := pal.sub
	if hex, err := strconv.ParseUint(it.Color, 16, 32); err == nil && len(it.Color) == 6 {
		c = color.RGBA{uint8(hex >> 16), uint8(hex >> 8), uint8(hex), 255}
	}
	face := fc.value
	if it.Kind == "clock" {
		face, c = fc.labelBold, pal.text
	}
	w := r.width(face, text)
	isz := 26
	if it.Icon != "" {
		w += r.s(isz) + r.s(6)
	}
	if drawIt {
		x := right - w
		if it.Icon != "" {
			r.mdiIcon(it.Icon, x, base-r.s(isz)+r.s(4), isz, c)
			x += r.s(isz) + r.s(6)
		}
		r.text(face, text, x, base, c)
	}
	return w
}

// agoText is how long ago something was, in a few words.
func agoText(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d d ago", int(d.Hours()/24))
}

// tessTile draws one tile in its cells: the badge with its icon at the top, and the name and what it is
// doing at the foot, larger as the tile is. A reading - a sensor's - is the tile's largest words.
// It says where the tile's controls are, if it has any drawn.
func (r *paint) tessTile(b image.Rectangle, t tessera.Placed, pal dashPal, adj dashAdjusting) []dashTile {
	card := pal.card
	if c, ok := swatches[t.Background]; ok {
		card = lerp(pal.card, c, 0.24)
	}
	if t.Confirm {
		card = lerp(pal.card, swatches["red"], 0.3)
	}
	r.roundFill(b, pal.rad, card, card)

	// A level: a bar along the foot shows where it is, and is the sign a finger can slide it. While one
	// does, the whole tile fills to the level instead.
	value := t.Value
	sliding := t.Level != nil && adj.entity == t.Entity
	controls := tessControlRows(t)
	if l := t.Level; l != nil && l.Max > l.Min {
		v := l.Value
		if sliding {
			v = adj.value
		}
		frac := tessFrac(v, l.Min, l.Max)
		a := dashboard.Adjust{Kind: l.Kind}
		if sliding && len(controls) == 0 {
			fill := image.Rect(b.Min.X, b.Min.Y, b.Min.X+int(float64(b.Dx())*frac), b.Max.Y)
			if fill.Dx() > 0 {
				c := lerp(card, pal.active, 0.35)
				r.roundFill(fill, pal.rad, c, c)
			}
			value = a.Label(v)
		}
		if !sliding && len(controls) == 0 && frac > 0 && (t.On || l.Kind == "temperature") {
			inset := r.s(16)
			c := pal.active
			if !t.On {
				c = lerp(card, pal.sub, 0.6)
			}
			bar := image.Rect(b.Min.X+inset, b.Max.Y-r.s(9), b.Min.X+inset+int(float64(b.Dx()-2*inset)*frac), b.Max.Y-r.s(5))
			if bar.Dx() > 0 {
				r.roundFill(bar, r.sf(2), c, c)
			}
		}
	}

	// Sizes grow with the tile: by its height against a usual cell's, as far as its width allows.
	k := min(float64(b.Dy())/float64(r.s(180)), float64(b.Dx())/float64(r.s(200)), 1.8)
	k = max(k, 0.7)
	if len(t.Days) > 0 && t.Rows >= 2 || len(controls) > 0 {
		k = min(k, 1.25) // the room is the forecast's, or the controls'
	}
	sc := func(n int) int { return int(float64(n)*k + 0.5) }
	pad := r.s(sc(16))
	badge := r.s(sc(52))
	ring := image.Rect(b.Min.X+pad, b.Min.Y+pad, b.Min.X+pad+badge, b.Min.Y+pad+badge)
	// A tile too low for its badge over its words (a grid of many rows) has the words beside the badge,
	// both in the middle of its height, as Tessera's own small cards do.
	compact := len(controls) == 0 && !(len(t.Days) > 0 && t.Rows >= 2) && b.Dy() < 2*pad+badge+r.s(sc(66))
	if compact {
		ring = ring.Add(image.Pt(0, (b.Dy()-badge)/2-pad))
	}
	ic := pal.sub
	switch {
	case t.Gone:
		ic = lerp(pal.sub, card, 0.5)
		r.roundFill(ring, float64(badge)/2, lerp(card, pal.sub, 0.12), lerp(card, pal.sub, 0.12))
	case t.On:
		ic = pal.active
		r.roundFill(ring, float64(badge)/2, lerp(card, pal.active, 0.22), lerp(card, pal.active, 0.22))
	default:
		r.roundFill(ring, float64(badge)/2, lerp(card, pal.sub, 0.14), lerp(card, pal.sub, 0.14))
	}
	r.mdiIconIn(t.Icon, ring, sc(30), ic)

	// A switch at the top right, for something a tap turns on and off; a lock asking to be sure says
	// what the second tap does there.
	// The button where it fits beside the badge: the tint and the words say it on a narrow tile.
	pill := t.Confirm && b.Max.X-pad-r.width(r.faces().button, "Unlock")-r.s(44) > ring.Max.X+r.s(8)
	switch {
	case pill:
		r.pillButton(b.Max.X-pad, ring.Min.Y+badge/2, "Unlock", btnDanger)
	case t.Confirm:
	case t.Taps && toggles(t.Entity) && !t.Gone:
		sw := image.Rect(b.Max.X-pad-r.s(sc(46)), ring.Min.Y+(badge-r.s(sc(26)))/2, b.Max.X-pad, ring.Min.Y+(badge+r.s(sc(26)))/2)
		r.tessSwitch(sw, t.On, card, pal)
	}

	room := b.Dx() - 2*pad
	// The controls the add-on says the card has, as Tessera's own screens draw them: under the words, a
	// row each, on a card with the room, else the first beside the words on a card one row high and
	// two or more wide.
	var zones []dashTile
	textBottom := b.Max.Y
	if len(controls) > 0 {
		rowH, gap := r.s(sc(48)), r.s(sc(10))
		free := b.Dy() - 2*pad - badge - r.s(sc(64))
		// Larger keys where there is room for them, on a page-sized card: two thirds of what is free.
		rowH = min(max((free*2/3-gap*(len(controls)-1))/len(controls), rowH), r.s(sc(64)))
		if n := min(len(controls), (free+gap)/(rowH+gap)); n > 0 {
			top := b.Max.Y - pad - n*rowH - (n-1)*gap
			for j, row := range controls[:n] {
				y := top + j*(rowH+gap)
				zones = append(zones, r.tessControl(row, image.Rect(b.Min.X+pad, y, b.Max.X-pad, y+rowH), t, pal, card, adj, k)...)
			}
			textBottom = top - gap + pad
		} else if t.Cols >= 2 {
			split := b.Min.X + b.Dx()*2/5
			zones = r.tessControl(controls[0], image.Rect(split, b.Max.Y-pad-rowH, b.Max.X-pad, b.Max.Y-pad), t, pal, card, adj, k)
			room = split - b.Min.X - pad - r.s(8)
		}
	}
	// Room under the words for a level's bar, on every tile, so that a row's words line up.
	foot := textBottom - pad - r.s(4)
	name, sub := r.textFace(false, sc(25)), r.textFace(false, sc(20))
	big := r.textFace(true, sc(40))
	if name == nil || sub == nil || big == nil {
		return zones
	}
	textC := pal.text
	if t.Gone {
		textC = lerp(pal.text, card, 0.4)
	}
	// What a reading's words under it say: its name, and for the weather what it is doing, whose
	// temperature is the reading.
	reading, under := value, t.Name
	if tesseraDomain(t.Entity) == "weather" {
		if temp, cond, ok := strings.Cut(value, " · "); ok {
			reading, under = temp, t.Name+" · "+cond
		}
	}
	// A forecast where the tile has room for one: beside the words on a tile one row high; under the
	// badge, with the reading beside it, on a taller one.
	if len(t.Days) > 0 && !sliding {
		switch {
		case t.Rows == 1 && t.Cols >= 2:
			split := b.Min.X + b.Dx()*2/5
			r.tessDays(image.Rect(split, b.Min.Y+pad, b.Max.X-pad, b.Max.Y-pad), t.Days, false, pal, k)
			room = split - b.Min.X - pad - r.s(8)
		case t.Rows >= 2:
			x := ring.Max.X + r.s(sc(14))
			r.text(big, r.fit(big, reading, b.Max.X-pad-x), x, ring.Min.Y+badge*5/8, textC)
			r.text(sub, r.fit(sub, under, b.Max.X-pad-x), x, ring.Max.Y+r.s(sc(8)), pal.sub)
			r.tessDays(image.Rect(b.Min.X+pad, ring.Max.Y+r.s(sc(28)), b.Max.X-pad, b.Max.Y-pad), t.Days, t.Cols == 1, pal, k)
			return zones
		}
	}
	if compact {
		x := ring.Max.X + r.s(sc(12))
		right := b.Min.X + pad + room
		switch {
		case pill:
			right = min(right, b.Max.X-pad-r.width(r.faces().button, "Unlock")-r.s(52))
		case t.Taps && toggles(t.Entity) && !t.Gone:
			right = min(right, b.Max.X-pad-r.s(sc(46))-r.s(8))
		}
		top, low, first, second, firstC := name, sub, t.Name, value, textC
		if t.Reading && !sliding {
			top, first, second = big, reading, under
		}
		gap := r.s(sc(9))
		ha, hb := top.Metrics().Ascent.Ceil(), low.Metrics().Ascent.Ceil()
		y := ring.Min.Y + (badge-(ha+gap+hb))/2 + ha
		r.text(top, r.fit(top, first, right-x), x, y, firstC)
		r.text(low, r.fit(low, second, right-x), x, y+gap+hb, pal.sub)
		return zones
	}
	if t.Reading && !sliding {
		// The reading large, the name under it.
		r.text(sub, r.fit(sub, under, room), b.Min.X+pad, foot, pal.sub)
		r.text(big, r.fit(big, reading, room), b.Min.X+pad, foot-r.s(sc(30)), textC)
		return zones
	}
	if pill && r.width(sub, value) > room {
		value = "Tap again" // the button beside the badge says what for
	}
	r.text(sub, r.fit(sub, value, room), b.Min.X+pad, foot, pal.sub)
	lines := r.wrapLines(name, t.Name, room)
	lh := r.s(sc(30))
	// Two lines of name where the tile is tall enough for them under the badge.
	most := 1
	// The first line's top is about two thirds of a line over its baseline.
	if foot-r.s(sc(28))-lh-r.s(sc(20)) > ring.Max.Y+r.s(sc(4)) && len(lines) > 1 {
		most = 2
		if len(lines) > 2 {
			lines[1] = r.fit(name, strings.Join(lines[1:], " "), room)
		}
	}
	if most == 1 {
		lines = []string{r.fit(name, t.Name, room)}
	}
	lines = lines[:min(len(lines), most)]
	y := foot - r.s(sc(28)) - (len(lines)-1)*lh
	for _, line := range lines {
		r.text(name, line, b.Min.X+pad, y, textC)
		y += lh
	}
	return zones
}

// tessControlRows are the rows of controls of t that can be drawn with what the tile says: a level for
// a setpoint or a slider, modes for a mode row, a volume for a player's.
func tessControlRows(t tessera.Placed) []string {
	var out []string
	for _, row := range t.Controls {
		switch {
		case row == "mode" && len(t.Modes) == 0:
		case row == "volume" && t.Volume < 0:
		case (row == "setpoint" || row == "brightness" || row == "position" || row == "speed") && t.Level == nil:
		default:
			out = append(out, row)
		}
	}
	return out
}

// tessControl draws one row of a tile's controls in a, and says where its keys and slider are: a
// player's back, play or pause and next; a cover's open, stop and close; a thermostat's step down and
// up around what it is set to, or its modes; a level as a bar a finger slides. k is the tile's scale.
func (r *paint) tessControl(row string, a image.Rectangle, t tessera.Placed, pal dashPal, card color.RGBA, adj dashAdjusting, k float64) []dashTile {
	sc := func(n int) int { return int(float64(n)*k + 0.5) }
	zone := func(at image.Rectangle, ctl string) dashTile {
		return dashTile{r: at, entity: t.Entity, tess: t.I + 1, ctl: ctl}
	}
	key := lerp(card, pal.sub, 0.16)
	button := func(at image.Rectangle, icon string, lit bool) {
		c, ic := key, pal.text
		if lit {
			c, ic = pal.accent, pal.bg
		}
		r.roundFill(at, float64(at.Dy())/2, c, c)
		r.mdiIconIn(icon, at, sc(26), ic)
	}
	keys := func(icons, ctls []string, lit []bool) []dashTile {
		gap := r.s(sc(10))
		w := min((a.Dx()-gap*(len(icons)-1))/len(icons), a.Dy()*2)
		x := a.Min.X + (a.Dx()-len(icons)*w-(len(icons)-1)*gap)/2
		var zones []dashTile
		for j := range icons {
			at := image.Rect(x, a.Min.Y, x+w, a.Max.Y)
			button(at, icons[j], lit[j])
			zones = append(zones, zone(at, ctls[j]))
			x += w + gap
		}
		return zones
	}
	switch row {
	case "playback":
		play := "play"
		if t.On {
			play = "pause"
		}
		return keys([]string{"skip-previous", play, "skip-next"}, []string{"prev", "play", "next"}, []bool{false, t.On, false})
	case "buttons":
		return keys([]string{"arrow-up", "stop", "arrow-down"}, []string{"open", "stop", "close"}, []bool{false, false, false})
	case "setpoint":
		// Kept together: the steps either side of the number, not at the far ends of a page.
		if extra := a.Dx() - a.Dy()*7; extra > 0 {
			a.Min.X, a.Max.X = a.Min.X+extra/2, a.Max.X-extra/2
		}
		w := min(a.Dy()*3/2, a.Dx()/3)
		down, up := image.Rect(a.Min.X, a.Min.Y, a.Min.X+w, a.Max.Y), image.Rect(a.Max.X-w, a.Min.Y, a.Max.X, a.Max.Y)
		button(down, "minus", false)
		button(up, "plus", false)
		v := t.Level.Value
		if adj.entity == t.Entity {
			v = adj.value
		}
		face := r.textFace(true, int(float64(sc(30))*float64(a.Dy())/float64(max(r.s(sc(48)), 1))+0.5))
		if face != nil {
			s := strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64) + "°"
			r.text(face, s, a.Min.X+(a.Dx()-r.width(face, s))/2, a.Min.Y+a.Dy()/2+a.Dy()*11/48, pal.text)
		}
		return []dashTile{zone(down, "down"), zone(up, "up")}
	case "mode":
		face := r.textFace(false, sc(18))
		if face == nil {
			return nil
		}
		gap := r.s(sc(8))
		n := min(len(t.Modes), max((a.Dx()+gap)/(r.s(sc(76))+gap), 1))
		w := (a.Dx() - gap*(n-1)) / n
		var zones []dashTile
		for j, m := range t.Modes[:n] {
			at := image.Rect(a.Min.X+j*(w+gap), a.Min.Y, a.Min.X+j*(w+gap)+w, a.Max.Y)
			c, tc := key, pal.text
			if m == t.State || t.State == "" && m == "off" {
				c, tc = pal.accent, pal.bg
			}
			r.roundFill(at, float64(at.Dy())/2, c, c)
			s := r.fit(face, climateWord(m), w-r.s(sc(12)))
			r.text(face, s, at.Min.X+(w-r.width(face, s))/2, at.Min.Y+at.Dy()/2+r.s(sc(7)), tc)
			zones = append(zones, zone(at, "mode:"+m))
		}
		return zones
	}
	// A level: a bar the width of the row, filled to where it is.
	l := dashboard.Adjust{Entity: t.Entity}
	icon := "brightness-6"
	switch row {
	case "volume":
		l.Kind, l.Value, l.Max, l.Step, icon = "volume", math.Round(t.Volume*100), 100, 1, "volume-high"
	default:
		l.Kind, l.Value, l.Min, l.Max, l.Step = t.Level.Kind, t.Level.Value, t.Level.Min, t.Level.Max, t.Level.Step
		icon = map[string]string{"position": "window-shutter-open", "speed": "fan"}[row]
		if icon == "" {
			icon = "brightness-6"
		}
	}
	v := l.Value
	if adj.entity == t.Entity {
		v = adj.value
	}
	if l.Max <= l.Min {
		return nil
	}
	frac := tessFrac(v, l.Min, l.Max)
	r.roundFill(a, float64(a.Dy())/2, key, key)
	if fill := image.Rect(a.Min.X, a.Min.Y, a.Min.X+int(float64(a.Dx())*frac), a.Max.Y); fill.Dx() > 0 {
		c := lerp(card, pal.active, 0.55)
		r.roundFill(fill, float64(a.Dy())/2, c, c)
	}
	r.mdiIconIn(icon, image.Rect(a.Min.X+r.s(sc(6)), a.Min.Y, a.Min.X+r.s(sc(6))+a.Dy(), a.Max.Y), sc(24), pal.text)
	if face := r.textFace(true, sc(18)); face != nil {
		s := l.Label(v)
		r.text(face, s, a.Max.X-r.s(sc(16))-r.width(face, s), a.Min.Y+a.Dy()/2+r.s(sc(7)), pal.text)
	}
	z := zone(a, "")
	z.adjust = &l
	return []dashTile{z}
}

// tessDays draws a forecast in b, as many days as fit: side by side, each its name, its weather and its
// high and low, or one a line when down is set. k is the tile's scale.
func (r *paint) tessDays(b image.Rectangle, days []tessera.Day, down bool, pal dashPal, k float64) {
	sc := func(n int) int { return int(float64(n)*k + 0.5) }
	name, hi := r.textFace(false, sc(18)), r.textFace(true, sc(20))
	if name == nil || hi == nil || b.Dx() <= 0 || b.Dy() <= 0 {
		return
	}
	temp := func(d tessera.Day) (string, string) {
		h, l := "", ""
		if d.HasHigh {
			h = strconv.FormatFloat(math.Round(d.High), 'f', 0, 64) + "°"
		}
		if d.HasLow {
			l = strconv.FormatFloat(math.Round(d.Low), 'f', 0, 64) + "°"
		}
		return h, l
	}
	if down {
		// A line a day, spread over the room up to half as far apart again.
		line := r.s(sc(40))
		n := min(len(days), b.Dy()/max(line, 1))
		if n > 0 {
			line = min(b.Dy()/n, r.s(sc(60)))
		}
		// The names in a column as wide as the widest, the temperatures at the right, and the weather
		// in the middle of what is between.
		names := 0
		for _, d := range days[:n] {
			names = max(names, r.width(name, d.Name))
		}
		names = min(names, b.Dx()/3)
		for i, d := range days[:n] {
			y := b.Min.Y + i*line
			base := y + line/2 + r.s(sc(7))
			r.text(name, r.fit(name, d.Name, names), b.Min.X, base, pal.sub)
			h, l := temp(d)
			x := b.Max.X
			if l != "" {
				x -= r.width(name, l)
				r.text(name, l, x, base, pal.sub)
				x -= r.s(sc(8))
			}
			x -= r.width(hi, h)
			r.text(hi, h, x, base, pal.text)
			if gap := image.Rect(b.Min.X+names, y, x, y+line).Inset(r.s(sc(4))); gap.Dx() >= r.s(sc(20)) {
				// At most two thirds of the gap, in the Show 5's pixels that icon sizes are in.
				r.mdiIconIn(d.Icon, gap, min(sc(26), gap.Dx()*66/max(r.s(100), 1)), pal.text)
			}
		}
		return
	}
	col := r.s(sc(62))
	n := min(len(days), b.Dx()/max(col, 1))
	if n == 0 {
		return
	}
	col = b.Dx() / n
	icon := min(b.Dy()/2, r.s(sc(40)), b.Dy()-r.s(sc(22))-r.s(sc(44)))
	if icon < r.s(sc(16)) {
		return // no room for a day's weather
	}
	top := b.Min.Y + (b.Dy()-(r.s(sc(22))+icon+r.s(sc(44))))/2
	for i, d := range days[:n] {
		x := b.Min.X + i*col
		center := func(face font.Face, s string, y int, c color.RGBA) {
			s = r.fit(face, s, col)
			r.text(face, s, x+(col-r.width(face, s))/2, y, c)
		}
		center(name, d.Name, top+r.s(sc(18)), pal.sub)
		r.mdiIconIn(d.Icon, image.Rect(x, top+r.s(sc(22)), x+col, top+r.s(sc(22))+icon), sc(30), pal.text)
		h, l := temp(d)
		center(hi, h, top+r.s(sc(22))+icon+r.s(sc(20)), pal.text)
		center(name, l, top+r.s(sc(22))+icon+r.s(sc(42)), pal.sub)
	}
}

// tessFrac is how far v is from lo to hi, from 0 to 1: 0 for a value that is not a number, which would
// otherwise come to any width at all.
func tessFrac(v, lo, hi float64) float64 {
	f := (v - lo) / (hi - lo)
	if math.IsNaN(f) {
		return 0
	}
	return min(max(f, 0), 1)
}

// toggles is whether an entity's tap turns it on and off, which its tile shows with a switch.
func toggles(entity string) bool {
	switch tesseraDomain(entity) {
	case "light", "switch", "input_boolean", "fan", "automation", "humidifier", "siren":
		return true
	}
	return false
}

func tesseraDomain(entity string) string {
	for i := range len(entity) {
		if entity[i] == '.' {
			return entity[:i]
		}
	}
	return entity
}

// tessSwitch draws an on/off switch in sw, as a row of the drawn dashboard has one.
func (r *paint) tessSwitch(sw image.Rectangle, on bool, card color.RGBA, pal dashPal) {
	track := lerp(card, pal.sub, 0.35)
	if on {
		track = lerp(card, pal.accent, 0.6)
	}
	r.roundFill(sw, float64(sw.Dy())/2, track, track)
	in := max(r.s(3), 2)
	knob := sw.Dy() - 2*in
	kx := sw.Min.X + in
	kc := lerp(pal.text, card, 0.1)
	if on {
		kx, kc = sw.Max.X-in-knob, pal.accent
	}
	k := image.Rect(kx, sw.Min.Y+in, kx+knob, sw.Min.Y+in+knob)
	r.roundFill(k, float64(knob)/2, kc, kc)
}

// tessTap is a tap on the Tessera page: a tile does what it is for, a dot or the way back goes to its
// page, and a tap on none of them starts a voice turn, as a tap on the clock does while Tap on the clock
// is Assist.
func (d *Display) tessTap(x, y int) {
	if d.colorTap(x, y) {
		return // a sheet is up, and takes it
	}
	t := d.tileAt(x, y)
	switch {
	case t == nil:
		if config.Get().Screen.ClockTap == "" {
			tessAssist()
		}
	case t.page > 0:
		d.tessGo(t.page - 1)
	case t.tess > 0 && t.ctl != "":
		d.tessControl(t.tess-1, t.entity, t.ctl)
	case t.tess > 0:
		if tessera.Tap(t.tess-1, t.entity) {
			d.tessSheet(t.entity)
		}
	}
}

// tessAssist starts a voice turn, or answers or ends the one under way, where there are microphones to
// take one; a variable for the tests.
var tessAssist = func() {
	if hasMicrophone() {
		voice.Get().Action()
	}
}

// tessRoute is the player a play or pause, next or back on a player go to: Music Assistant's while it
// plays there, as on the drawn dashboard. A variable so that a test asks no Home Assistant.
var tessRoute = dashboard.TransportTo

// A tap on a player's tile plays or pauses it where its keys do.
func init() {
	tessera.Transport = func(entity string) string { return tessRoute(entity) }
}

// tessKeys are a player's keys, by the controls' names for them.
var tessKeys = map[string]string{"prev": "media_player.media_previous_track", "play": "media_player.media_play_pause",
	"next": "media_player.media_next_track"}

// tessControl is a tap on one of tile i's controls. A player's keys go where the drawn dashboard's do,
// worked out off the finger's goroutine.
func (d *Display) tessControl(i int, entity, ctl string) {
	if service, ok := tessKeys[ctl]; ok {
		slog.Info("tessera control", "entity", entity, "service", service)
		go func() { tessera.Call(tessRoute(entity), service, nil) }()
		return
	}
	tessera.Control(i, entity, ctl)
}

// tessGo shows page p.
func (d *Display) tessGo(p int) {
	d.mu.Lock()
	d.tessPage = p
	d.mu.Unlock()
	d.wake()
}

// tessHold is a finger coming down on the Tessera page; what it does is decided as it moves.
func (d *Display) tessHold(x, y int) {
	if d.colorOpen() {
		d.drawnHold(x, y) // the sheet's, as on the drawn dashboard
		return
	}
	t := d.tileAt(x, y)
	d.mu.Lock()
	d.dashDrag.at, d.dashDrag.tile, d.dashDrag.down = image.Pt(x, y), t, true
	d.mu.Unlock()
}

// tessMove is the finger moving: along a tile with a level, the level follows it; across anything else,
// the page follows when it lifts.
func (d *Display) tessMove(x, y int) {
	if d.colorOpen() {
		d.drawnMove(x, y)
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	dr := &d.dashDrag
	dx, dy := x-dr.at.X, y-dr.at.Y
	if abs(dx) > holdStill || abs(dy) > holdStill {
		dr.moved = true
	}
	if !dr.sliding && !dr.scrolling && abs(dx) > d.r.s(slideStart) && abs(dx) > abs(dy) {
		if dr.tile != nil && dr.tile.adjust != nil {
			dr.sliding, dr.value = true, dr.tile.adjust.Value
			dr.at.X = x // measured from here, so the level does not jump by the slack
			dx = 0
		} else {
			dr.scrolling = true // a swipe across the page
		}
	}
	if dr.sliding {
		a := dr.tile.adjust
		dr.value = a.Snap(a.Value + float64(dx)/float64(dr.tile.r.Dx())*(a.Max-a.Min))
		d.dashAdjust = dashAdjusting{entity: a.Entity, value: dr.value}
	}
}

// tessRelease is the finger lifting: a slide sets its level, a swipe far enough turns to the next page
// or the one before, of those the dots show, and a finger that stayed put until it was a hold opens the
// tile's sheet.
func (d *Display) tessRelease(x, y int) {
	if d.colorOpen() {
		d.drawnRelease(x, y)
		return
	}
	d.mu.Lock()
	dr := d.dashDrag
	d.dashDrag = drawnDrag{}
	d.dashAdjust = dashAdjusting{}
	page := d.tessPage
	d.mu.Unlock()
	switch {
	case dr.sliding && dr.tile != nil && dr.tile.adjust != nil && dr.tile.tess > 0:
		slog.Info("tessera slide", "entity", dr.tile.adjust.Entity, "to", dr.value)
		tessera.SetLevel(dr.tile.tess-1, dr.tile.entity, dr.value)
	case dr.scrolling && abs(x-dr.at.X) > d.r.s(tessSwipe):
		v, _ := tessera.Current()
		step := 1
		if x > dr.at.X {
			step = -1
		}
		if next, ok := turnPage(v, page, step); ok {
			d.tessGo(next)
		}
	case dr.down && !dr.moved && !dr.sliding && !dr.scrolling && dr.tile != nil && dr.tile.tess > 0:
		d.tessSheet(dr.tile.entity)
	}
	d.wake()
}

// turnPage is the page step pages on from page among those the dots show, and false past either end. A
// detail page turns to the home page's neighbors as if it were the home page.
func turnPage(v tessera.View, page, step int) (int, bool) {
	var shown []int
	at := -1
	for i, p := range v.Pages {
		if p.Excluded {
			continue
		}
		if i == page || page < len(v.Pages) && v.Pages[page].Excluded && i == v.Home {
			at = len(shown)
		}
		shown = append(shown, i)
	}
	next := at + step
	if at < 0 || next < 0 || next >= len(shown) {
		return 0, false
	}
	return shown[next], true
}
