//go:build !dot

package dashboard

import (
	"fmt"
	"image/color"
	"regexp"
	"strconv"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// A picture-elements card's elements: places on its picture, each with a tap of its own, so that one
// picture drawn for the screen can be a whole page of buttons. They are placed as Home Assistant
// places them, so the same card works the same on both: an element is position: absolute in the
// picture, its style's left and top in percent, and Home Assistant's own transform,
// translate(-50%, -50%), centers it on that point (hui-picture-elements-card's .element). An icon
// shows its icon, a state icon and a state label their entity, a service button its title; an image
// and a state badge are only their tap here, the picture being what shows them. An element its style
// makes invisible is drawn as nothing, and is still its tap; one it takes off the page (display none,
// visibility hidden) is neither, as in Home Assistant. Any other element, a custom one or one placed in
// pixels or from the right or the bottom, is left out, and the rest of the card is drawn as usual.

// element is one element as the card gives it.
type element struct {
	kind                      string
	entity                    string
	x, y, w, h                float64
	sx, sy                    float64
	icon, title               string
	attribute, prefix, suffix string
	tap                       *Action
	conds                     []cond // a conditional element's, all of them met for it to be there
	color                     color.RGBA
	hidden                    bool
}

// elementKinds are the elements the device places; anything else is left out.
var elementKinds = map[string]bool{
	"icon": true, "image": true, "state-icon": true, "state-label": true, "state-badge": true, "service-button": true,
}

// elements reads a card's elements, those in a conditional element too, behind its conditions as
// well as conds.
func (c *compiler) elements(v any, conds []cond) []element {
	list, _ := v.([]any)
	var out []element
	for _, it := range list {
		m, ok := it.(raw)
		if !ok {
			continue
		}
		kind := str(m, "type")
		if kind == "conditional" {
			inner := append(append([]cond{}, conds...), c.conditions(m["conditions"], "")...)
			out = append(out, c.elements(m["elements"], inner)...)
			continue
		}
		if !elementKinds[kind] {
			continue
		}
		style, _ := m["style"].(raw)
		if cssValue(style, "display") == "none" || cssValue(style, "visibility") == "hidden" {
			continue // not drawn, and no tap in Home Assistant either
		}
		x, okx := percent(style["left"])
		y, oky := percent(style["top"])
		if !okx || !oky {
			continue
		}
		e := element{kind: kind, entity: str(m, "entity"), x: x, y: y, icon: str(m, "icon"), title: str(m, "title"),
			attribute: str(m, "attribute"), prefix: str(m, "prefix"), suffix: str(m, "suffix"), conds: conds}
		e.w, _ = percent(style["width"])
		e.h, _ = percent(style["height"])
		e.sx, e.sy = shiftOf(style)
		e.color, e.hidden = colorOf(style)
		if (kind == "state-icon" || kind == "state-label") && e.entity == "" {
			continue
		}
		if kind == "state-icon" || kind == "state-label" {
			c.follow(e.entity)
		}
		e.tap = pictureTap(m, e.entity)
		if kind == "service-button" {
			e.tap = serviceButton(m)
		}
		out = append(out, e)
	}
	return out
}

// serviceButton is what a service button element runs: its action and data, as the card writes them.
func serviceButton(m raw) *Action {
	service := str(m, "action")
	if service == "" {
		service = str(m, "service")
	}
	if !strings.Contains(service, ".") {
		return nil
	}
	data, _ := m["data"].(raw)
	if data == nil {
		data, _ = m["service_data"].(raw)
	}
	out := map[string]any{}
	for k, v := range data {
		out[k] = v
	}
	if target, ok := m["target"].(raw); ok {
		for k, v := range target {
			out[k] = v
		}
	}
	return &Action{Service: service, Data: out}
}

// percent is a style's length in percent, as a fraction: "17%" is 0.17. A zero needs no unit; pixels
// and anything else are not read, as there is no telling what they come to on another screen.
func percent(v any) (float64, bool) {
	s, ok := v.(string)
	if !ok {
		if f, ok := v.(float64); ok && f == 0 {
			return 0, true
		}
		return 0, false
	}
	s = strings.TrimSpace(s)
	if s == "0" {
		return 0, true
	}
	n, isPct := strings.CutSuffix(s, "%")
	if !isPct {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
	if err != nil {
		return 0, false
	}
	return f / 100, true
}

var translate = regexp.MustCompile(`(translate[XY]?)\(([^)]*)\)`)

// shiftOf is how far an element's style moves it by its own size: Home Assistant's translate(-50%,
// -50%) when it gives no transform, and the transform's own translates when it gives one, which
// replaces Home Assistant's: translate(x, y), translate(x) with no move down, translateX(x) and
// translateY(y), each moving it on its own axis only. A move in pixels is taken as none, as is a
// transform without a translate ("none", a scale): there is no telling what pixels come to here.
func shiftOf(style raw) (float64, float64) {
	t, ok := style["transform"].(string)
	if !ok {
		return -0.5, -0.5
	}
	var x, y float64
	for _, m := range translate.FindAllStringSubmatch(t, -1) {
		args := strings.Split(m[2], ",")
		first, _ := percent(args[0])
		switch m[1] {
		case "translate":
			x += first
			if len(args) > 1 {
				second, _ := percent(args[1])
				y += second
			}
		case "translateX":
			x += first
		case "translateY":
			y += first
		}
	}
	return x, y
}

// namedColors are the few colors a style is likely to name rather than write out.
var namedColors = map[string]color.RGBA{
	"white": {255, 255, 255, 255}, "black": {0, 0, 0, 255}, "red": {255, 0, 0, 255}, "green": {0, 128, 0, 255},
	"blue": {0, 0, 255, 255}, "yellow": {255, 255, 0, 255}, "orange": {255, 165, 0, 255}, "gray": {128, 128, 128, 255},
	"grey": {128, 128, 128, 255},
}

// cssValue is a style's value as CSS reads it: in any case, spaces and an !important left off.
func cssValue(style raw, key string) string {
	v, _, _ := strings.Cut(str(style, key), "!")
	return strings.ToLower(strings.TrimSpace(v))
}

// colorOf is an element's style's color, zero for none it can read, and whether the style makes it
// invisible: opacity 0 or a transparent color. Home Assistant draws an element whatever its color, and
// taps one of opacity 0, so only those leave it a tap with nothing drawn.
func colorOf(style raw) (color.RGBA, bool) {
	hidden := false
	switch o := style["opacity"].(type) {
	case float64:
		hidden = o == 0
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(o), "%"), 64); err == nil {
			hidden = f == 0
		}
	}
	v := strings.ToLower(strings.TrimSpace(str(style, "color")))
	if transparent(v) {
		return color.RGBA{}, true
	}
	if c, ok := namedColors[v]; ok {
		return c, hidden
	}
	c, _ := parseColor(v)
	return c, hidden
}

// transparent is whether a CSS color is see-through: transparent, an alpha of 0 in rgba() or hsla()
// (or after a slash, "rgb(0 0 0 / 0)"), or a hex color whose alpha is 0 (#rgb0, #rrggbb00). Three
// values in rgba() are a color with no alpha, not one of alpha 0.
func transparent(v string) bool {
	if v == "transparent" {
		return true
	}
	if h, ok := strings.CutPrefix(v, "#"); ok {
		return len(h) == 4 && h[3] == '0' || len(h) == 8 && h[6:] == "00"
	}
	open, end := strings.IndexByte(v, '('), strings.LastIndexByte(v, ')')
	if open < 0 || end < open {
		return false
	}
	switch strings.TrimSpace(v[:open]) {
	case "rgb", "rgba", "hsl", "hsla":
	default:
		return false
	}
	args := v[open+1 : end]
	var alpha string
	if _, a, ok := strings.Cut(args, "/"); ok {
		alpha = a
	} else if parts := strings.Split(args, ","); len(parts) == 4 {
		alpha = parts[3]
	} else {
		return false
	}
	f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(alpha), "%"), 64)
	return err == nil && f == 0
}

// at is the element as it stands, or false while its conditions are not met.
func (e element) at(l look) (Element, bool) {
	if !all(e.conds, l) {
		return Element{}, false
	}
	out := Element{X: e.x, Y: e.y, W: e.w, H: e.h, ShiftX: e.sx, ShiftY: e.sy, Entity: e.entity, Tap: e.tap,
		Color: e.color, Hidden: e.hidden}
	switch e.kind {
	case "icon":
		out.Icon = e.icon
	case "state-icon", "state-label":
		st, ok := l.states[e.entity]
		if !ok {
			st = hass.LiveEntity{ID: e.entity, State: "unknown", Attrs: map[string]any{}}
		}
		t := describe(st, "", l.precision)
		out.On, out.Gone = t.On, t.Gone
		if e.kind == "state-icon" {
			out.Icon = orIcon(e.icon, t.Icon)
			break
		}
		v := t.Value
		if e.attribute != "" {
			v = ""
			if a, ok := st.Attrs[e.attribute]; ok && a != nil {
				v = fmt.Sprint(a)
			}
		}
		out.Label = e.prefix + v + e.suffix
	case "service-button":
		out.Label = e.title
	}
	return out, true
}
