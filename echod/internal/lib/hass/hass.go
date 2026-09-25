// Package hass is Home Assistant's REST API, for what the ESPHome link cannot carry: a forecast
// (a service that answers), pictures, cameras. It needs a long-lived token, handed to the device
// once through an action and kept on userdata.
package hass

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// Path is where the URL and token live: next to the PSK, readable by root only.
var Path = filepath.Join(filepath.Dir(layout.KeyPath), "hass.json")

type access struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

type Client struct {
	mu   sync.Mutex
	acc  access
	http *http.Client
}

var (
	once   sync.Once
	shared *Client
)

// Get is the client; it reads the saved access the first time.
func Get() *Client {
	once.Do(func() {
		shared = &Client{http: &http.Client{Timeout: 15 * time.Second}}
		if b, err := os.ReadFile(Path); err == nil {
			_ = json.Unmarshal(b, &shared.acc)
			// Kept before the cleaning below existed, an address can carry a character nobody can see.
			shared.acc.URL, shared.acc.Token = cleanURL(shared.acc.URL), clean(shared.acc.Token)
		}
	})
	return shared
}

// Set stores the URL (like http://192.168.1.20:8123) and token.
func (c *Client) Set(url, token string) error {
	url, token = cleanURL(url), clean(token)
	if url == "" || token == "" {
		return errors.New("hass: url and token are both needed")
	}
	if u, err := neturl.Parse(url); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("hass: %q is not an address like http://homeassistant.local:8123", url)
	}
	c.mu.Lock()
	c.acc = access{URL: url, Token: token}
	c.mu.Unlock()
	b, _ := json.Marshal(c.acc)
	if err := os.MkdirAll(filepath.Dir(Path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(Path, b, 0o600)
}

// clean takes out what a copy and paste carries along without anyone seeing it: spaces and line ends,
// and the invisible ones - a zero-width space, a byte order mark - that no trim removes. Neither an
// address nor a token has any of them, and one in front of "http" made every request fail with
// "first path segment in URL cannot contain colon", which says nothing about why (techo5#24).
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) || !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, s)
}

// cleanURL is an address cleaned, without the trailing slash the paths are joined to.
func cleanURL(s string) string { return strings.TrimRight(clean(s), "/") }

// Ready reports whether there is an access to use.
func (c *Client) Ready() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.acc.URL != "" && c.acc.Token != ""
}

func (c *Client) do(method, path string, body any) ([]byte, error) {
	c.mu.Lock()
	acc := c.acc
	c.mu.Unlock()
	if acc.URL == "" || acc.Token == "" {
		return nil, errors.New("hass: no access configured")
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, acc.URL+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+acc.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("hass: %s %s: %s", method, path, resp.Status)
	}
	return out, nil
}

// Day is one day of a forecast.
type Day struct {
	When      time.Time
	Condition string
	High      float64
	Low       float64
	Rain      int // precipitation probability, percent, -1 when not given
}

// Forecast is the daily forecast for a weather entity, as many days as it gives.
func (c *Client) Forecast(entity string) ([]Day, error) {
	out, err := c.do("POST", "/api/services/weather/get_forecasts?return_response",
		map[string]any{"entity_id": entity, "type": "daily"})
	if err != nil {
		return nil, err
	}
	var resp struct {
		ServiceResponse map[string]struct {
			Forecast []struct {
				Datetime      string   `json:"datetime"`
				Condition     string   `json:"condition"`
				Temperature   *float64 `json:"temperature"`
				Templow       *float64 `json:"templow"`
				Precipitation *float64 `json:"precipitation_probability"`
			} `json:"forecast"`
		} `json:"service_response"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, err
	}
	fc, ok := resp.ServiceResponse[entity]
	if !ok {
		return nil, fmt.Errorf("hass: no forecast for %s", entity)
	}
	days := make([]Day, 0, len(fc.Forecast))
	for _, f := range fc.Forecast {
		d := Day{Condition: f.Condition, Rain: -1}
		if t, err := time.Parse(time.RFC3339, f.Datetime); err == nil {
			d.When = t.Local()
		}
		if f.Temperature != nil {
			d.High = *f.Temperature
		}
		if f.Templow != nil {
			d.Low = *f.Templow
		}
		if f.Precipitation != nil {
			d.Rain = int(*f.Precipitation)
		}
		days = append(days, d)
	}
	return days, nil
}

// State is an entity's state and attributes.
type State struct {
	State      string         `json:"state"`
	Attributes map[string]any `json:"attributes"`
}

func (c *Client) State(entity string) (State, error) {
	out, err := c.do("GET", "/api/states/"+entity, nil)
	if err != nil {
		return State{}, err
	}
	var s State
	return s, json.Unmarshal(out, &s)
}

// Fetch gets bytes from a path on Home Assistant (an entity_picture, a camera_proxy image).
func (c *Client) Fetch(path string) ([]byte, error) {
	return c.do("GET", path, nil)
}

// FetchURL gets bytes from a URL a resolve returned: a path relative to Home Assistant, one of its
// own absolute URLs, or an absolute URL an external source already signed. Only the first two get
// the token. The third is a host the user does not run — Jellyfin, Plex, Synology Photos, a radio
// directory, a cloud photo service — and the token is a long-lived one that opens the whole Home
// Assistant API, so putting it on that request would hand the house to whoever answers it. Those
// fetches go out bare, which is all they need: the source signed the URL itself.
func (c *Client) FetchURL(url string) ([]byte, error) {
	if strings.HasPrefix(url, "/") {
		return c.do("GET", url, nil)
	}
	if rest, ok := ownURL(c.baseURL(), url); ok {
		return c.do("GET", rest, nil)
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("hass: GET %s: %s", url, resp.Status)
	}
	return out, nil
}

// ownURL reports whether raw addresses the configured Home Assistant, and if so returns the part
// after the base for do to send along with the token. It parses both sides instead of comparing
// the strings: a prefix test has no boundary, so with a base of http://ha:8123 a URL like
// http://ha:8123.evil.example/x reads as one of ours and the token walks out to a stranger's host.
// Scheme and host compared as net/url splits them cannot be misread that way, and the path still
// needs a / after the base so that a Home Assistant living under /ha does not lend the token to
// /hacked.
func ownURL(base, raw string) (string, bool) {
	if base == "" {
		return "", false
	}
	b, err := neturl.Parse(base)
	if err != nil || b.Host == "" {
		return "", false
	}
	u, err := neturl.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, b.Scheme) ||
		!strings.EqualFold(hostPort(u), hostPort(b)) {
		return "", false
	}
	rest, prefix := u.EscapedPath(), strings.TrimRight(b.EscapedPath(), "/")
	if prefix != "" {
		if rest != prefix && !strings.HasPrefix(rest, prefix+"/") {
			return "", false
		}
		rest = strings.TrimPrefix(rest, prefix)
	}
	if rest == "" {
		rest = "/"
	}
	if u.RawQuery != "" {
		rest += "?" + u.RawQuery
	}
	return rest, true
}

// hostPort is a URL's host with the port its scheme implies when the URL leaves it out, so that a
// Home Assistant configured as http://ha and a URL of http://ha:80/... are recognized as the one host
// they are; written as strings they are not equal, and the token would stay behind on a request that
// then fails. It does not loosen the boundary ownURL exists for: host and port still come from the
// parser and are compared whole, so a name that only starts like ours is still another host, and any
// port other than the scheme's default is still another port.
func hostPort(u *neturl.URL) string {
	host, port := u.Hostname(), u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}
	return host + ":" + port
}

// baseURL is the configured Home Assistant URL, empty when none is set.
func (c *Client) baseURL() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.acc.URL
}

// Entity is an entity's id and the name Home Assistant shows for it.
type Entity struct {
	ID, Name string
}

// Entities lists the entities of a domain ("weather"), in Home Assistant's order.
func (c *Client) Entities(domain string) ([]Entity, error) {
	out, err := c.do("GET", "/api/states", nil)
	if err != nil {
		return nil, err
	}
	var states []struct {
		EntityID   string         `json:"entity_id"`
		Attributes map[string]any `json:"attributes"`
	}
	if err := json.Unmarshal(out, &states); err != nil {
		return nil, err
	}
	var list []Entity
	for _, s := range states {
		if !strings.HasPrefix(s.EntityID, domain+".") {
			continue
		}
		name, _ := s.Attributes["friendly_name"].(string)
		list = append(list, Entity{ID: s.EntityID, Name: name})
	}
	return list, nil
}

// MusicAssistantFor finds Music Assistant's own media player for a device's media player: the one
// whose active queue is it. Empty when there is none.
func (c *Client) MusicAssistantFor(own string) (string, error) {
	out, err := c.do("GET", "/api/states", nil)
	if err != nil {
		return "", err
	}
	var states []struct {
		EntityID   string         `json:"entity_id"`
		Attributes map[string]any `json:"attributes"`
	}
	if err := json.Unmarshal(out, &states); err != nil {
		return "", err
	}
	for _, s := range states {
		if !strings.HasPrefix(s.EntityID, "media_player.") || s.EntityID == own {
			continue
		}
		app, _ := s.Attributes["app_id"].(string)
		queue, _ := s.Attributes["active_queue"].(string)
		if app == "music_assistant" && queue == own {
			return s.EntityID, nil
		}
	}
	return "", nil
}

// Render has Home Assistant render a template and returns the text.
func (c *Client) Render(template string) (string, error) {
	out, err := c.do("POST", "/api/template", map[string]any{"template": template})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Call runs a Home Assistant action.
func (c *Client) Call(domain, service string, data map[string]any) error {
	_, err := c.do("POST", "/api/services/"+domain+"/"+service, data)
	return err
}

// MediaPlay asks a media player to play again what it was playing.
func (c *Client) MediaPlay(player string) error {
	_, err := c.do("POST", "/api/services/media_player/media_play", map[string]any{"entity_id": player})
	return err
}

// PlayMedia asks a media player to play something: a URL, or a media-source:// id that Home Assistant
// resolves (and converts for the player) itself.
func (c *Client) PlayMedia(player, id, kind string) error {
	if kind == "" {
		kind = "music"
	}
	_, err := c.do("POST", "/api/services/media_player/play_media", map[string]any{
		"entity_id": player, "media_content_id": id, "media_content_type": kind,
	})
	return err
}

// Config is the part of Home Assistant's configuration the device uses.
type Config struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Country   string  `json:"country"`
}

func (c *Client) Config() (Config, error) {
	out, err := c.do("GET", "/api/config", nil)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	return cfg, json.Unmarshal(out, &cfg)
}
