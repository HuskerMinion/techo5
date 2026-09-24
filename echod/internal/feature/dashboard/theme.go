//go:build !dot

package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"regexp"
	"strconv"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// Theme is the colors a drawn dashboard takes from Home Assistant's own theme, so the page looks like
// the house's dashboards rather than like the device.
type Theme struct {
	Set        bool // false: none could be read, and the page keeps the device's own colors
	Background color.RGBA
	Card       color.RGBA
	Text       color.RGBA
	Sub        color.RGBA // secondary text
	Accent     color.RGBA // Home Assistant's primary color: switches, graphs, levels
	Active     color.RGBA // a light that is on, anything active
	Radius     int        // a card's corner, in CSS pixels
}

// haDefault is Home Assistant's own dark look, for a house with no theme of its own.
var haDefault = map[string]string{
	"primary-background-color":  "#111111",
	"card-background-color":     "#1c1c1c",
	"primary-text-color":        "#e1e1e1",
	"secondary-text-color":      "#9b9b9b",
	"primary-color":             "#03a9f4",
	"state-icon-active-color":   "#fdd835",
	"ha-card-border-radius":     "12px",
	"ha-color-primary-40":       "#009ac7",
	"ha-color-text-primary":     "#e1e1e1",
	"ha-color-text-secondary":   "#9b9b9b",
	"state-light-on-color":      "#ff9800",
	"state-active-color":        "#ff9800",
	"ha-card-background":        "var(--card-background-color)",
	"card-background-color-alt": "#1c1c1c",
}

// loadTheme reads the theme Home Assistant's frontend uses by default, in its dark mode where it has
// one, since a screen in a room at night wants dark.
func loadTheme(ctx context.Context, live *hass.Live) Theme {
	raw, err := live.Call(ctx, map[string]any{"type": "frontend/get_themes"})
	if err != nil {
		return Theme{}
	}
	var got struct {
		Themes  map[string]map[string]any `json:"themes"`
		Default string                    `json:"default_theme"`
		Dark    string                    `json:"default_dark_theme"`
	}
	if json.Unmarshal(raw, &got) != nil {
		return Theme{}
	}
	name := got.Dark
	if name == "" {
		name = got.Default
	}
	vars := map[string]string{}
	for k, v := range haDefault {
		vars[k] = v
	}
	if t, ok := got.Themes[name]; ok {
		flat(t, vars)
		if modes, ok := t["modes"].(map[string]any); ok {
			if dark, ok := modes["dark"].(map[string]any); ok {
				flat(dark, vars)
			}
		}
	}
	return themeOf(vars)
}

func flat(from map[string]any, into map[string]string) {
	for k, v := range from {
		if s, ok := v.(string); ok {
			into[k] = s
		}
	}
}

func themeOf(vars map[string]string) Theme {
	pick := func(names ...string) (color.RGBA, bool) {
		for _, n := range names {
			if c, ok := parseColor(resolve(vars, vars[n], 0)); ok {
				return c, true
			}
		}
		return color.RGBA{}, false
	}
	t := Theme{Set: true, Radius: 12}
	var ok bool
	if t.Background, ok = pick("primary-background-color"); !ok {
		return Theme{}
	}
	t.Card, _ = pick("ha-card-background", "card-background-color")
	t.Text, _ = pick("primary-text-color", "ha-color-text-primary")
	t.Sub, _ = pick("secondary-text-color", "ha-color-text-secondary")
	t.Accent, _ = pick("primary-color", "ha-color-primary-40")
	t.Active, _ = pick("state-light-on-color", "state-icon-active-color", "state-active-color", "primary-color")
	if r := resolve(vars, vars["ha-card-border-radius"], 0); r != "" {
		if n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(r), "px")); err == nil {
			t.Radius = n
		}
	}
	return t
}

var varRef = regexp.MustCompile(`^var\(\s*--([\w-]+)\s*(?:,\s*(.+))?\)$`)

// resolve follows var(--name) through the theme to a value.
func resolve(vars map[string]string, v string, depth int) string {
	v = strings.TrimSpace(v)
	if depth > 10 {
		return ""
	}
	if m := varRef.FindStringSubmatch(v); m != nil {
		if next, ok := vars[m[1]]; ok {
			return resolve(vars, next, depth+1)
		}
		return resolve(vars, m[2], depth+1)
	}
	return v
}

var rgbFn = regexp.MustCompile(`^rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)`)

// parseColor reads #rgb, #rrggbb, rgb() and rgba(), and a bare "r, g, b", which themes use too.
func parseColor(s string) (color.RGBA, bool) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "#") {
		h := s[1:]
		if len(h) == 3 {
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		}
		if len(h) >= 6 {
			var r, g, b uint8
			if _, err := fmt.Sscanf(h[:6], "%02x%02x%02x", &r, &g, &b); err == nil {
				return color.RGBA{r, g, b, 255}, true
			}
		}
		return color.RGBA{}, false
	}
	if m := rgbFn.FindStringSubmatch(s); m != nil {
		return rgb(m[1], m[2], m[3])
	}
	if parts := strings.Split(s, ","); len(parts) == 3 {
		return rgb(parts[0], parts[1], parts[2])
	}
	return color.RGBA{}, false
}

func rgb(r, g, b string) (color.RGBA, bool) {
	var out [3]uint8
	for i, p := range []string{r, g, b} {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 0 || n > 255 {
			return color.RGBA{}, false
		}
		out[i] = uint8(n)
	}
	return color.RGBA{out[0], out[1], out[2], 255}, true
}
