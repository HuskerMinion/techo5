package hass

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

// Board is one dashboard view a screen could show: how a list names it, and its path as Home
// Assistant's address bar has it ("lovelace/0", "dashboard-kitchen/lights").
type Board struct {
	Label string `json:"label"`
	Path  string `json:"path"`
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
	return out, nil
}
