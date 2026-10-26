//go:build !dot && !spot

package display

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/image/font"

	"github.com/HuskerMinion/techo5/echod/internal/feature/tessera"
)

// A Tessera plugin's tile: on a Tessera board the plugin is code built into the firmware that draws it; here
// the display draws the tiles of the plugins the screen names as its own (tessera.builtInPlugins) from the
// options the add-on sends, and any other plugin's as a card that says it is missing, as a board does.

// tessPlugin draws a plugin tile in b, on its card.
func (r *paint) tessPlugin(b image.Rectangle, t tessera.Placed, pal dashPal, card color.RGBA, now time.Time) {
	switch {
	case t.Plugin == "world_clock" && t.PluginTile == "clocks":
		r.worldClock(b.Inset(r.s(10)), t.PluginOptions, pal, card, now)
		return
	case t.Plugin == "custom_card" && t.PluginTile == "card":
		parts, note := ccParts(t.PluginData)
		r.customCard(b.Inset(r.s(14)), t.Name, parts, note, t.Rows == 1 && t.Cols >= 2, t.PluginOptions, pal, card)
		return
	}
	fc := r.faces()
	pad := r.s(cardPad + 4)
	r.text(fc.labelBold, r.fit(fc.labelBold, t.Name, b.Dx()-2*pad), b.Min.X+pad, b.Min.Y+pad+fc.labelBold.Metrics().Ascent.Ceil(), pal.text)
	r.text(fc.sub, r.fit(fc.sub, "Plugin missing", b.Dx()-2*pad), b.Min.X+pad, b.Max.Y-pad, pal.sub)
}

// tessPluginTap is a tap on a plugin tile: a world clock turns its dials to digits and back, as the
// plugin's tap turns its switch; another plugin's tile takes none.
func tessPluginTap(plugin string) bool {
	if plugin != "world_clock" {
		return false
	}
	wcDigits.Store(!wcDigits.Load())
	return true
}

// wcDigits is the world clock's Digits instead of dials, which a tap on its tile turns over.
var wcDigits atomic.Bool

// The World clock plugin (github.com/stofle/tessera-plugin-world-clock 0.4.0, MIT), drawn as its
// world_clock.cpp draws it: up to three places side by side, each its name, its clock (a dial, classic or
// calm, or the time in digits) and how far it is from the screen's time. The clock takes the largest
// size its column leaves; the offset line stays only where it costs the clock nothing, else the offset
// follows the name. Its words are its translations' (English: the plugin has no Spanish).
var wcWords = map[string]string{
	"offset": "%s h", "same": "Same time", "tomorrow": "Tomorrow", "yesterday": "Yesterday",
	"z_local": "Here", "z_pacific_honolulu": "Honolulu", "z_america_anchorage": "Anchorage",
	"z_america_los_angeles": "Los Angeles", "z_america_denver": "Denver", "z_america_chicago": "Chicago",
	"z_america_mexico_city": "Mexico City", "z_america_new_york": "New York", "z_america_toronto": "Toronto",
	"z_america_bogota": "Bogotá", "z_america_lima": "Lima", "z_america_santiago": "Santiago",
	"z_america_sao_paulo": "São Paulo", "z_america_argentina_buenos_aires": "Buenos Aires",
	"z_atlantic_reykjavik": "Reykjavík", "z_europe_london": "London", "z_europe_dublin": "Dublin",
	"z_europe_lisbon": "Lisbon", "z_europe_madrid": "Madrid", "z_europe_paris": "Paris",
	"z_europe_amsterdam": "Amsterdam", "z_europe_berlin": "Berlin", "z_europe_zurich": "Zurich",
	"z_europe_rome": "Rome", "z_europe_vienna": "Vienna", "z_europe_stockholm": "Stockholm",
	"z_europe_warsaw": "Warsaw", "z_europe_athens": "Athens", "z_europe_helsinki": "Helsinki",
	"z_europe_istanbul": "Istanbul", "z_europe_moscow": "Moscow", "z_africa_cairo": "Cairo",
	"z_africa_lagos": "Lagos", "z_africa_johannesburg": "Johannesburg", "z_africa_nairobi": "Nairobi",
	"z_asia_dubai": "Dubai", "z_asia_karachi": "Karachi", "z_asia_kolkata": "Mumbai",
	"z_asia_bangkok": "Bangkok", "z_asia_singapore": "Singapore", "z_asia_shanghai": "Shanghai",
	"z_asia_hong_kong": "Hong Kong", "z_asia_seoul": "Seoul", "z_asia_tokyo": "Tokyo",
	"z_australia_perth": "Perth", "z_australia_sydney": "Sydney", "z_pacific_auckland": "Auckland",
}

// wcPlace is one place of a world clock.
type wcPlace struct {
	name string
	loc  *time.Location
	here bool // the screen's own time
}

// wcZoneKey is the key of a zone's name in wcWords: "z_america_new_york" for "America/New_York".
func wcZoneKey(id string) string {
	var b strings.Builder
	b.WriteString("z_")
	for _, c := range id {
		switch {
		case c >= 'A' && c <= 'Z':
			b.WriteRune(c - 'A' + 'a')
		case c >= 'a' && c <= 'z' || c >= '0' && c <= '9':
			b.WriteRune(c)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// wcPlaces are the places a world clock's options choose, in order; a zone this device has no rules
// for is left out, as the plugin leaves out one it does not know.
func wcPlaces(o map[string]any) []wcPlace {
	defaults := []string{"America/New_York", "local", "Europe/London"}
	var out []wcPlace
	for i := range 3 {
		id, ok := o[fmt.Sprintf("zone_%d", i+1)].(string)
		if !ok {
			id = defaults[i]
		}
		p := wcPlace{here: id == "local"}
		switch {
		case id == "none":
			continue
		case p.here:
			p.loc = time.Local
		default:
			loc := cachedZone(id) // read once, not every frame
			if loc == nil {
				continue
			}
			p.loc = loc
		}
		p.name, _ = o[fmt.Sprintf("name_%d", i+1)].(string)
		if p.name = strings.TrimSpace(p.name); p.name == "" {
			p.name = wcWords[wcZoneKey(id)]
		}
		if p.name == "" {
			p.name = id
		}
		out = append(out, p)
	}
	return out
}

// wcAway is how far a place is from the screen's time: "-6 h", "+5:30 h", "Same time", and with
// days "Tomorrow · +8 h".
func wcAway(diff time.Duration, days int) string {
	text := wcWords["same"]
	if diff != 0 {
		sign, m := "+", int(diff.Abs()/time.Minute)
		if diff < 0 {
			sign = "-"
		}
		n := fmt.Sprintf("%s%d", sign, m/60)
		if m%60 != 0 {
			n = fmt.Sprintf("%s%d:%02d", sign, m/60, m%60)
		}
		text = fmt.Sprintf(wcWords["offset"], n)
	}
	switch {
	case days > 0:
		return wcWords["tomorrow"] + " · " + text
	case days < 0:
		return wcWords["yesterday"] + " · " + text
	}
	return text
}

// wcDigitFaces are the faces a world clock's digits may take, largest first, kept by font.
var wcDigitFaces = struct {
	sync.Mutex
	by map[string][]font.Face
}{by: map[string][]font.Face{}}

func (r *paint) wcFaces() []font.Face {
	key := fmt.Sprintf("%s/%d/%d", r.font, r.fontGen, r.s(100))
	wcDigitFaces.Lock()
	defer wcDigitFaces.Unlock()
	if f, ok := wcDigitFaces.by[key]; ok {
		return f
	}
	var f []font.Face
	for _, px := range []int{72, 56, 44, 34, 28, 24} {
		f = append(f, fontFace(r.font, false, float64(r.s(px))))
	}
	wcDigitFaces.by[key] = f
	return f
}

// worldClock draws the World clock's clocks tile into b.
func (r *paint) worldClock(b image.Rectangle, o map[string]any, pal dashPal, card color.RGBA, now time.Time) {
	places := wcPlaces(o)
	if len(places) == 0 || b.Dx() <= 0 || b.Dy() <= 0 {
		return
	}
	face, _ := o["face"].(string)
	if face == "" {
		face = "analog"
	}
	hand, _ := o["second_hand"].(string)
	if hand == "" {
		hand = "home"
	}
	home := 0
	for i, p := range places {
		if p.here {
			home = i
			break
		}
	}

	fc := r.faces()
	n := len(places)
	gap := r.s(16)
	column := (b.Dx() - (n-1)*gap) / n
	nameH := fc.sub.Metrics().Height.Ceil()
	lineGap := r.s(6)
	roomWith, roomWithout := b.Dy()-2*(nameH+lineGap), b.Dy()-nameH-lineGap
	// The largest digits a column takes, for the shape of a time.
	shape := strings.Map(func(c rune) rune {
		if c >= '1' && c <= '9' {
			return '0'
		}
		return c
	}, clockText(time.Date(2000, 1, 1, 12, 0, 0, 0, time.UTC)))
	faces := r.wcFaces()
	fitting := func(room int) font.Face {
		for _, f := range faces {
			if r.width(f, shape) <= column && f.Metrics().Height.Ceil() <= room {
				return f
			}
		}
		return fc.sub
	}
	digits := fitting(roomWith)
	digitsOffset := digits == fitting(roomWithout)
	mm := func(n float64) int { return int(n * pxPerMM) }
	dials := face != "digital" && min(column, roomWithout) >= mm(6)
	withOffset, clockH := digitsOffset, digits.Metrics().Height.Ceil()
	dial := 0
	if dials {
		dialOffset := min(column, roomWith) >= min(column, roomWithout)
		dial = min(column, roomWithout)
		if dialOffset {
			dial = min(column, roomWith)
		}
		if !wcDigits.Load() {
			withOffset, clockH = dialOffset, dial
		}
	}
	showDials := dials && !wcDigits.Load()
	block := nameH + lineGap + clockH
	if withOffset && block+lineGap+nameH <= b.Dy() {
		block += lineGap + nameH
	} else {
		withOffset = false // no room for its line under the clock: the offset follows the name
	}
	top := b.Min.Y + max(0, (b.Dy()-block)/2)
	clockY := top + nameH + lineGap

	_, hereOff := now.Zone()
	today := wcDay(now)
	center := func(f font.Face, s string, x, y int, c color.RGBA) {
		r.text(f, s, x+(column-r.width(f, s))/2, y+f.Metrics().Ascent.Ceil(), c)
	}
	for i, p := range places {
		x := b.Min.X + i*(column+gap)
		at := now.In(p.loc)
		_, off := at.Zone()
		diff := time.Duration(off-hereOff) * time.Second
		away, hours := wcAway(diff, wcDay(at)-today), wcAway(diff, 0)
		switch {
		case withOffset:
			center(fc.sub, r.fit(fc.sub, p.name, column), x, top, pal.sub)
			line := away
			if r.width(fc.sub, away) > column {
				line = hours
			}
			center(fc.sub, r.fit(fc.sub, line, column), x, clockY+clockH+lineGap, pal.sub)
		case diff == 0:
			center(fc.sub, r.fit(fc.sub, p.name, column), x, top, pal.sub)
		default:
			// No line for it: the offset after the name.
			name := p.name + "  " + away
			if r.width(fc.sub, name) > column {
				name = p.name + "  " + hours
			}
			if r.width(fc.sub, name) > column {
				name = p.name // the name before its offset, rather than both cut short
			}
			center(fc.sub, r.fit(fc.sub, name, column), x, top, pal.sub)
		}
		if !showDials {
			center(digits, clockText(at), x, clockY, pal.text)
			continue
		}
		// Automatic: the calm face on a small dial, where twelve marks would crowd it.
		calm := face == "calm" || face == "analog" && dial < mm(18)
		seconds := hand == "all" || hand == "home" && i == home
		cx, cy := float64(x)+float64(column)/2, float64(clockY)+float64(dial)/2
		r.wcDial(cx, cy, dial, calm, seconds, at, pal, card)
	}
}

// pxPerMM is about how many pixels a millimeter of an Echo Show's panel is, the Show 8's and the Show 5's
// alike: what the plugin's sizes in millimeters are measured in.
const pxPerMM = 7.4

// wcDay is the day of t in its own zone, counted from 1970.
func wcDay(t time.Time) int {
	y, m, d := t.Date()
	return int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

// wcDial is a dial dial pixels across at cx, cy showing t: classic, as Tessera's own analog clock (twelve
// marks, numerals at the quarters where the ring has room), or calm (a disc in the ink, four strokes and a
// dot at the other hours); the hour hand, the minute hand in the accent, a thin red second hand.
func (r *paint) wcDial(cx, cy float64, dial int, calm, seconds bool, t time.Time, pal dashPal, card color.RGBA) {
	ink, muted, accent, red := pal.text, pal.sub, pal.accent, swatches["red"]
	secs := float64(t.Hour()*3600 + t.Minute()*60 + t.Second())
	hand := func(turn, length, tail, w float64, c color.RGBA, radius float64) {
		a := turn * 2 * math.Pi
		s, co := math.Sin(a), math.Cos(a)
		r.aaLine(cx-radius*tail*s, cy+radius*tail*co, cx+radius*length*s, cy-radius*length*co, w, c)
	}
	if calm {
		rad := float64(dial)/2 - 1
		r.aaDisc(cx, cy, rad, ink)
		inset, stroke := math.Max(2, rad*0.09), math.Max(3, rad*0.22)
		strokeW, dot := math.Max(2, math.Round(rad*0.08)), math.Max(2, math.Round(rad*0.075))
		for i := range 12 {
			a := float64(i) * math.Pi / 6
			s, co := math.Sin(a), math.Cos(a)
			if i%3 == 0 {
				r.aaLine(cx+(rad-inset)*s, cy-(rad-inset)*co, cx+(rad-inset-stroke)*s, cy-(rad-inset-stroke)*co, strokeW, card)
				continue
			}
			ring := rad - inset - stroke*0.3
			r.aaDisc(cx+ring*s, cy-ring*co, dot/2, muted)
		}
		hand(math.Mod(secs, 43200)/43200, 0.5, 0, math.Max(3, math.Round(rad*0.16)), card, rad)
		hand(math.Mod(secs, 3600)/3600, 0.78, 0, math.Max(2, math.Round(rad*0.11)), accent, rad)
		if seconds {
			hand(math.Mod(secs, 60)/60, 0.92*0.9, 0.2, math.Max(1, math.Round(rad*0.045)), red, rad)
		}
		r.aaDisc(cx, cy, math.Max(5, math.Round(rad*0.26))/2, accent)
		return
	}
	outer := float64(dial)/2 - 1
	radius := float64(dial)/2 - float64(r.s(4))
	fc := r.faces()
	box := float64(fc.sub.Metrics().Height.Ceil() + 4)
	numeralRing := outer - 11
	numerals := numeralRing >= box+float64(r.s(2))
	for i := range 12 {
		a := float64(i) * math.Pi / 6
		s, co := math.Sin(a), math.Cos(a)
		cardinal := i%3 == 0
		if cardinal && numerals {
			n := "12"
			if i > 0 {
				n = fmt.Sprint(i)
			}
			nx, ny := cx+numeralRing*s, cy-numeralRing*co
			m := fc.sub.Metrics()
			r.text(fc.sub, n, int(nx)-r.width(fc.sub, n)/2, int(ny)+(m.Ascent.Ceil()-m.Descent.Ceil())/2, muted)
			continue
		}
		length, w := float64(r.s(5)), float64(r.s(2))
		if cardinal {
			length, w = float64(r.s(9)), float64(r.s(3))
		}
		r.aaLine(cx+outer*s, cy-outer*co, cx+(outer-length)*s, cy-(outer-length)*co, w, muted)
	}
	hand(math.Mod(secs, 43200)/43200, 0.52, 0, float64(r.s(5)), ink, radius)
	hand(math.Mod(secs, 3600)/3600, 0.82, 0, float64(r.s(3)), accent, radius)
	if seconds {
		hand(math.Mod(secs, 60)/60, 0.92, 0.2, float64(r.s(2)), red, radius)
	}
	r.aaDisc(cx, cy, float64(r.s(8))/2, accent)
}
