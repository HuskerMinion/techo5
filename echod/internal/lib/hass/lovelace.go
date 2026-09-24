package hass

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
)

// Board is one dashboard view a screen could show: how a list names it, and its path as Home
// Assistant's address bar has it ("lovelace/0", "dashboard-kitchen/lights").
type Board struct {
	Label string `json:"label"`
	Path  string `json:"path"`
	// Streamed is one of Home Assistant's built-in pages - Energy, History - which only a browser can
	// show: it is not made of cards the device could read.
	Streamed bool `json:"streamed,omitempty"`
}

// builtIn is the built-in pages worth a screen of their own, by the panel's name, as a list shows
// them. Settings, the profile and the like are left out.
var builtIn = map[string]string{
	"energy": "Energy", "history": "History", "logbook": "Logbook", "light": "Lights", "climate": "Climate",
	"security": "Security", "home": "Home", "maintenance": "Maintenance", "media-browser": "Media",
}

// Boards is every dashboard's views, the default dashboard first, over one connection. A dashboard
// whose configuration cannot be read is listed by its name alone, which opens its first view.
func (c *Client) Boards(ctx context.Context) ([]Board, error) {
	s, err := c.wsOpen(ctx)
	if err != nil {
		return nil, err
	}
	defer s.conn.Close()

	raw, err := s.call(map[string]any{"type": "lovelace/dashboards/list"})
	if err != nil {
		return nil, err
	}
	var listed []struct {
		URLPath string `json:"url_path"`
		Title   string `json:"title"`
		Mode    string `json:"mode"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return nil, err
	}

	type dash struct{ title, path string }
	dashes := []dash{{"Overview", ""}}
	for _, d := range listed {
		dashes = append(dashes, dash{d.Title, d.URLPath})
	}

	var out []Board
	for _, d := range dashes {
		base := d.path
		if base == "" {
			base = "lovelace"
		}
		cmd := map[string]any{"type": "lovelace/config"}
		if d.path != "" {
			cmd["url_path"] = d.path
		}
		var cfg struct {
			Views []struct {
				Title string `json:"title"`
				Path  string `json:"path"`
			} `json:"views"`
		}
		raw, err := s.call(cmd)
		if err != nil || json.Unmarshal(raw, &cfg) != nil || len(cfg.Views) <= 1 {
			// One view, none (a dashboard Home Assistant generates), or unreadable: the dashboard is
			// the choice.
			out = append(out, Board{Label: d.title, Path: base})
			continue
		}
		for i, v := range cfg.Views {
			name := strings.TrimSpace(v.Title)
			if name == "" {
				name = "View " + strconv.Itoa(i+1)
			}
			p := v.Path
			if p == "" {
				p = strconv.Itoa(i)
			}
			out = append(out, Board{Label: d.title + " · " + name, Path: base + "/" + p})
		}
	}

	// Home Assistant's own pages, after the dashboards, in a steady order.
	if raw, err := s.call(map[string]any{"type": "get_panels"}); err == nil {
		var panels map[string]struct {
			Component string `json:"component_name"`
		}
		if json.Unmarshal(raw, &panels) == nil {
			var names []string
			for key, p := range panels {
				if _, ok := builtIn[p.Component]; ok && key == p.Component {
					names = append(names, key)
				}
			}
			slices.Sort(names)
			for _, key := range names {
				out = append(out, Board{Label: builtIn[key] + " (streamed only)", Path: key, Streamed: true})
			}
		}
	}
	return out, nil
}
