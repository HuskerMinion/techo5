package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// The browser is signed in as the token's user, and a device drives it by touch. Left alone, anyone
// with the key could open Settings or Developer Tools and work them through the page. So a device is
// shown dashboards and nothing else: the page it asks for has to be one, and the page is kept on
// dashboards after that, however it is tapped.
//
// What counts as a dashboard is what Home Assistant says its panels are, not a guess from the path:
// a dashboard can be called anything.

// shown are the kinds of panel a device may be shown: the dashboards, and Home Assistant's own pages
// that only show things.
var shown = map[string]bool{
	"lovelace": true, "energy": true, "history": true, "logbook": true, "map": true,
	"light": true, "climate": true, "security": true, "home": true, "maintenance": true,
	"media-browser": true, "calendar": true, "todo": true,
}

// Webpage dashboards (iframe panels) are not shown at all: they frame whatever they were set to,
// and people set them to their tools - a code editor, the ESPHome dashboard - which would then be
// worked by touch by anyone with the key.

// guard knows which paths are dashboards, asking Home Assistant again now and then.
type guard struct {
	cfg config

	mu      sync.Mutex
	allowed map[string]bool // a path's first part
	at      time.Time
}

const guardFresh = time.Minute

// panels is the first part of every path a device may be shown.
func (g *guard) panels(ctx context.Context) (map[string]bool, error) {
	g.mu.Lock()
	if g.allowed != nil && time.Since(g.at) < guardFresh {
		defer g.mu.Unlock()
		return g.allowed, nil
	}
	g.mu.Unlock()

	got, err := listPanels(ctx, g.cfg)
	if err != nil {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.allowed != nil {
			return g.allowed, nil // the last answer, rather than nothing, while Home Assistant is away
		}
		return nil, err
	}
	allowed := map[string]bool{}
	for path, p := range got {
		if shown[p.component] {
			allowed[path] = true
		}
	}
	g.mu.Lock()
	g.allowed, g.at = allowed, time.Now()
	g.mu.Unlock()
	return allowed, nil
}

// allows is whether a device may be shown path.
func (g *guard) allows(ctx context.Context, path string) (bool, error) {
	first, ok := firstPart(path)
	if !ok {
		return false, nil
	}
	allowed, err := g.panels(ctx)
	if err != nil {
		return false, err
	}
	return allowed[first], nil
}

// firstPart is a path's first part, the panel; false for a path that is not a plain one. Plain is
// letters, digits, '-', '_' and '/' and nothing else: no '%' to be decoded into something else, no
// dots, no whitespace a browser would drop - nothing a browser could read as another path than the
// one checked here.
func firstPart(path string) (string, bool) {
	if path == "" || strings.Contains(path, "//") {
		return "", false
	}
	for _, c := range path {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '/') {
			return "", false
		}
	}
	first, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	return first, first != ""
}

type panel struct{ component, url string }

// listPanels asks Home Assistant for its panels: path to what it is.
func listPanels(ctx context.Context, cfg config) (map[string]panel, error) {
	raw, err := haCall(ctx, cfg, "get_panels")
	if err != nil {
		return nil, err
	}
	var panels map[string]struct {
		URLPath   string `json:"url_path"`
		Component string `json:"component_name"`
		Config    struct {
			URL string `json:"url"`
		} `json:"config"`
	}
	if err := json.Unmarshal(raw, &panels); err != nil {
		return nil, err
	}
	out := map[string]panel{}
	for key, p := range panels {
		path := p.URLPath
		if path == "" {
			path = key
		}
		out[path] = panel{component: p.Component, url: p.Config.URL}
	}
	return out, nil
}

// warnIfAdmin says so in the log when the token is an administrator's, which the README advises
// against.
func warnIfAdmin(ctx context.Context, cfg config) {
	raw, err := haCall(ctx, cfg, "auth/current_user")
	if err != nil {
		slog.Warn("could not ask Home Assistant whose token this is", "err", err)
		return
	}
	var u struct {
		Name    string `json:"name"`
		IsAdmin bool   `json:"is_admin"`
	}
	if json.Unmarshal(raw, &u) == nil && u.IsAdmin {
		slog.Warn("HA_TOKEN belongs to an administrator: make a user that is not one for dashcast (README, Security)", "user", u.Name)
	}
}

// haCall runs one command on Home Assistant's websocket.
func haCall(ctx context.Context, cfg config, command string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(cfg.ha, "http") + "/api/websocket"
	c, resp, err := websocket.DefaultDialer.DialContext(ctx, url, http.Header{})
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("home assistant: %w", err)
	}
	defer c.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetReadDeadline(dl)
	}
	var msg struct {
		Type    string          `json:"type"`
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
	}
	if err := c.ReadJSON(&msg); err != nil {
		return nil, err
	}
	if err := c.WriteJSON(map[string]string{"type": "auth", "access_token": cfg.token}); err != nil {
		return nil, err
	}
	if err := c.ReadJSON(&msg); err != nil {
		return nil, err
	}
	if msg.Type != "auth_ok" {
		return nil, errors.New("home assistant refused the token")
	}
	if err := c.WriteJSON(map[string]any{"id": 1, "type": command}); err != nil {
		return nil, err
	}
	if err := c.ReadJSON(&msg); err != nil {
		return nil, err
	}
	if !msg.Success {
		return nil, errors.New("home assistant refused " + command)
	}
	return msg.Result, nil
}
