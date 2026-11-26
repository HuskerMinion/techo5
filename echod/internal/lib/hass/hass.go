// Package hass is Home Assistant's REST API, for what the ESPHome link cannot carry: a forecast
// (a service that answers), pictures, cameras. It needs a long-lived token, handed to the device
// once through an action and kept on userdata.
package hass

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/netguard"
)

// Path is where the URL and token live: next to the PSK, readable by root only.
var Path = filepath.Join(filepath.Dir(layout.KeyPath), "hass.json")

type access struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	// A sign-in's (signin.go): the refresh token the access token is renewed with, the client id it
	// was given to, and when the access token runs out. All empty for a long-lived token.
	Refresh  string    `json:"refresh_token,omitempty"`
	ClientID string    `json:"client_id,omitempty"`
	Expires  time.Time `json:"expires,omitzero"`
}

type Client struct {
	mu  sync.Mutex
	acc access
	// renew is held while a sign-in's token is renewed, so callers that find it running out renew it
	// once between them (signin.go); signedOut is Home Assistant having refused to.
	renew     sync.Mutex
	signedOut bool
	// refused is the token having been refused by Home Assistant on the last word from it (a REST
	// 401, a websocket auth_invalid), and cleared by any word it took. A device whose stored token
	// was revoked otherwise looks the same as one whose Home Assistant has not come up yet (#706).
	refused bool
	// closed: the access is being left (api's leave, a reset), so nothing is written back to the file
	// that leaving removes, though what is in memory serves until the restart (Close).
	closed bool
	http   *http.Client
	// outside fetches addresses on hosts that are not Home Assistant: its dialer refuses the device's
	// own addresses. Nil (a Client made by hand) falls back to http.
	outside *http.Client
}

// newClient is a client with both the token client and the guarded one outside hosts get.
func newClient(url, token string) *Client {
	return &Client{
		acc:     access{URL: url, Token: token},
		http:    &http.Client{Timeout: 15 * time.Second},
		outside: outsideClient(),
	}
}

// outsideClient dials through netguard, which checks the address actually connected to: after DNS and
// after each redirect, so a name that resolves to the device is refused as much as its number is.
func outsideClient() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         netguard.Dialer(10 * time.Second).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			MaxIdleConnsPerHost: 1,
			IdleConnTimeout:     30 * time.Second,
		},
	}
}

var (
	once   sync.Once
	shared *Client
)

// Get is the client; it reads the saved access the first time.
func Get() *Client {
	once.Do(func() {
		shared = newClient("", "")
		if b, err := os.ReadFile(Path); err == nil {
			_ = json.Unmarshal(b, &shared.acc)
			// Kept before the cleaning below existed, an address can carry a character nobody can see.
			shared.acc.URL, shared.acc.Token = cleanURL(shared.acc.URL), clean(shared.acc.Token)
			if u, err := neturl.Parse(shared.acc.URL); err == nil && u.Host != "" {
				shared.acc.URL = normalURL(u) // an upper-case scheme saved before Set wrote it lower
			}
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
	u, err := neturl.Parse(url)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		// Not quoted back: an address can carry a user and password, and errors reach the log.
		return errors.New("hass: the address is not one like http://homeassistant.local:8123")
	}
	// As it was parsed: "HTTP://ha:8123" is a working address, and the websocket, which swaps the
	// scheme's "http" for "ws", needs it written the way url.Parse writes it.
	url = normalURL(u)
	c.renew.Lock()
	defer c.renew.Unlock()
	c.mu.Lock()
	was := c.acc
	c.acc = access{URL: url, Token: token}
	c.signedOut, c.refused = false, false
	c.mu.Unlock()
	if err := c.save(); err != nil {
		return err
	}
	c.revokeLater(was) // a sign-in this token replaces leaves the user's profile
	return nil
}

// save writes the access as it is now.
func (c *Client) save() error {
	c.mu.Lock()
	b, _ := json.Marshal(c.acc)
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(Path), 0o755); err != nil {
		return err
	}
	// Written beside it and renamed over it, so a power cut mid-write leaves the old file, not half of
	// one: a sign-in's refresh token is in it, and there is no getting that back without a new sign-in.
	tmp := Path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, Path)
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

// normalURL is an address as url.Parse wrote it: the scheme lower case, which the websocket needs
// ("http" swapped for "ws"), and the path as it was escaped. Nothing else belongs in it.
func normalURL(u *neturl.URL) string {
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.EscapedPath(), "/")
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
	return c.doContext(context.Background(), method, path, body)
}

// doContext is do that gives up when ctx is done.
func (c *Client) doContext(ctx context.Context, method, path string, body any) ([]byte, error) {
	resp, out, err := c.request(ctx, method, path, body, nil)
	if err != nil {
		return nil, err
	}
	if err := statusError(method, path, resp); err != nil {
		return nil, err
	}
	return out, nil
}

// request is one request to Home Assistant with the token and hdr's headers, renewed and asked once
// more if a sign-in's token is refused, with the reply as it came: what its status means is the
// caller's (statusError), as a 304 is no failure to a fetch that asked whether a picture changed.
func (c *Client) request(ctx context.Context, method, path string, body any, hdr http.Header) (*http.Response, []byte, error) {
	acc, err := c.token(ctx, false)
	if err != nil {
		return nil, nil, err
	}
	if acc.URL == "" || acc.Token == "" {
		return nil, nil, errors.New("hass: no access configured")
	}
	var payload []byte
	if body != nil {
		if payload, err = json.Marshal(body); err != nil {
			return nil, nil, err
		}
	}
	resp, out, err := c.send(ctx, method, acc, path, payload, hdr)
	if err == nil && resp.StatusCode == http.StatusUnauthorized && acc.Refresh != "" {
		// A sign-in's token refused before its time (Home Assistant restarted with its clock off, say):
		// renewed, and asked once more.
		if acc, err = c.token(ctx, true); err != nil {
			return nil, nil, err
		}
		resp, out, err = c.send(ctx, method, acc, path, payload, hdr)
	}
	if err != nil {
		return nil, nil, err
	}
	c.heard(acc.Token, resp.StatusCode != http.StatusUnauthorized)
	return resp, out, nil
}

// statusError is what Home Assistant's reply to method on path means when it is not a success.
func statusError(method, path string, resp *http.Response) error {
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("hass: %s %s: %w", method, path, ErrNotFound)
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("hass: %s %s: %s", method, path, resp.Status)
	}
	return nil
}

// heard notes whether Home Assistant took token on its last word. An answer about a token since
// replaced (a request still on its way when new access was given) says nothing about the new one.
func (c *Client) heard(token string, took bool) {
	c.mu.Lock()
	if c.acc.Token == token {
		c.refused = !took
	}
	c.mu.Unlock()
}

// Refused says Home Assistant refused the device's access the last time it was asked: the stored token
// is there but no longer works, or a sign-in was refused its renewal (SignedOut). Only new access will
// do.
func (c *Client) Refused() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refused && c.acc.Token != "" || c.signedOut
}

// send is one request with acc's token, and the reply read whole.
func (c *Client) send(ctx context.Context, method string, acc access, path string, payload []byte, hdr http.Header) (*http.Response, []byte, error) {
	var rd io.Reader
	if payload != nil {
		rd = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, acc.URL+path, rd)
	if err != nil {
		return nil, nil, err
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	req.Header.Set("Authorization", "Bearer "+acc.Token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return resp, out, err
}

// ErrNotFound is Home Assistant saying there is no such thing: for a state, an entity it does not have.
var ErrNotFound = errors.New("404 Not Found")

// Day is one day of a forecast, or one hour or half day of one that comes by the hour or twice a day.
type Day struct {
	When      time.Time
	Condition string
	High      float64
	Low       float64
	Rain      int // precipitation probability, percent, -1 when not given
	// HasHigh and HasLow are whether the forecast gave them: an hour's has a temperature and no low.
	HasHigh, HasLow bool
	// Night is a twice-daily forecast's night (is_daytime false); a daily one has none.
	Night bool
}

// forecastEntry is one entry of a forecast as Home Assistant sends it, from weather.get_forecasts or
// weather/subscribe_forecast.
type forecastEntry struct {
	Datetime      string   `json:"datetime"`
	Condition     string   `json:"condition"`
	Temperature   *float64 `json:"temperature"`
	Templow       *float64 `json:"templow"`
	Precipitation *float64 `json:"precipitation_probability"`
	IsDaytime     *bool    `json:"is_daytime"`
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
			Forecast []forecastEntry `json:"forecast"`
		} `json:"service_response"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, err
	}
	fc, ok := resp.ServiceResponse[entity]
	if !ok {
		return nil, fmt.Errorf("hass: no forecast for %s", entity)
	}
	return forecastDays(fc.Forecast, 24*time.Hour), nil
}

// forecastDays is a forecast's entries as Days. step is how far apart entries are meant to be, for
// one whose time can't be read: it is put where it comes in the list, from now.
func forecastDays(list []forecastEntry, step time.Duration) []Day {
	days := make([]Day, 0, len(list))
	start := time.Now()
	if step >= 24*time.Hour {
		start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.Local)
	} else {
		start = start.Truncate(time.Hour)
	}
	for i, f := range list {
		d := Day{Condition: f.Condition, Rain: -1}
		if t, ok := forecastTime(f.Datetime); ok {
			d.When = t
		} else {
			// A day whose date can't be read is the day it comes in the list, today first: left as the
			// zero time, every such day read as a Monday.
			if step >= 24*time.Hour {
				d.When = start.AddDate(0, 0, i)
			} else {
				d.When = start.Add(time.Duration(i) * step)
			}
		}
		if f.Temperature != nil {
			d.High, d.HasHigh = *f.Temperature, true
		}
		if f.Templow != nil {
			d.Low, d.HasLow = *f.Templow, true
		}
		if f.Precipitation != nil {
			d.Rain = int(*f.Precipitation)
		}
		d.Night = f.IsDaytime != nil && !*f.IsDaytime
		days = append(days, d)
	}
	return days
}

// forecastStep is how far apart a forecast of a type's entries are.
func forecastStep(kind string) time.Duration {
	switch kind {
	case "hourly":
		return time.Hour
	case "twice_daily":
		return 12 * time.Hour
	}
	return 24 * time.Hour
}

// forecastLayouts are the forms a forecast's datetime comes in without a zone, as some integrations
// send it (Deutscher Wetterdienst's: 2026-10-23T00:00:00): with a T or a space, or the date alone.
// Those are read as local time, which is what the integration meant by midnight; read as UTC, a day
// west of Greenwich would be the day before.
var forecastLayouts = []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"}

// forecastTime is a forecast's datetime read: RFC 3339 as most integrations send it, with a space for
// the T, or without a zone.
func forecastTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, l := range []string{time.RFC3339, "2006-01-02 15:04:05Z07:00"} {
		if t, err := time.Parse(l, s); err == nil {
			return t.Local(), true
		}
	}
	for _, l := range forecastLayouts {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
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
	return c.FetchURLContext(context.Background(), url)
}

// FetchURLContext is FetchURL that gives up as soon as ctx is done, with the same limits and the same
// errors; a caller that no longer wants the bytes (a picture that has been replaced) does not hold its
// place for the client's whole timeout.
func (c *Client) FetchURLContext(ctx context.Context, url string) ([]byte, error) {
	resp, out, own, err := c.fetchURL(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	if err := fetchError(url, own, resp); err != nil {
		return nil, err
	}
	return out, nil
}

// Fetched is a picture as FetchFresh got it, with what its server said about keeping it.
type Fetched struct {
	Body []byte
	// NotModified is the server's 304: the bytes the ETag or Last-Modified asked about are still its
	// own, and Body is empty.
	NotModified bool
	// CacheControl, ETag and LastModified are the reply's headers of those names, as they came.
	CacheControl, ETag, LastModified string
}

// FetchFresh is FetchURLContext that also says what the server said about keeping the bytes, and asks
// whether they have changed when it is given what a fetch before said they were (etag, lastModified):
// a server that knows they have not answers 304 without sending them again. The token, the guarded
// client for other hosts and the size limit are FetchURL's.
func (c *Client) FetchFresh(ctx context.Context, url, etag, lastModified string) (Fetched, error) {
	hdr := http.Header{}
	if etag != "" {
		hdr.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		hdr.Set("If-Modified-Since", lastModified)
	}
	resp, out, own, err := c.fetchURL(ctx, url, hdr)
	if err != nil {
		return Fetched{}, err
	}
	f := Fetched{CacheControl: resp.Header.Get("Cache-Control"), ETag: resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified")}
	// Only a 304 to a question asked: one to a plain GET is a server confused, and nothing is kept to
	// stand for the bytes it did not send.
	if resp.StatusCode == http.StatusNotModified && len(hdr) > 0 {
		f.NotModified = true
		return f, nil
	}
	if err := fetchError(url, own, resp); err != nil {
		return Fetched{}, err
	}
	f.Body = out
	return f, nil
}

// fetchURL is one GET of url with hdr's headers, sent as FetchURL sends it, with the reply as it came
// and the path on Home Assistant it went to, empty for another host.
func (c *Client) fetchURL(ctx context.Context, url string, hdr http.Header) (*http.Response, []byte, string, error) {
	own := ""
	if strings.HasPrefix(url, "/") {
		own = url
	} else if rest, ok := ownURL(c.baseURL(), url); ok {
		own = rest
	}
	if own != "" {
		resp, out, err := c.request(ctx, "GET", own, nil, hdr)
		return resp, out, own, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, nil, "", err
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	hc := c.outside
	if hc == nil {
		hc = c.http
	}
	resp, err := hc.Do(req)
	if err != nil {
		var ue *neturl.Error
		if errors.As(err, &ue) {
			// The transport's own text repeats the whole address, login and query too.
			err = ue.Err
		}
		return nil, nil, "", fmt.Errorf("hass: GET %s: %w", withoutLogin(url), err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, nil, "", err
	}
	return resp, out, "", nil
}

// fetchError is what the reply to a fetch of url means when it is not a success: for a path on Home
// Assistant (own), as do says it; for another host, without the address's login or query.
func fetchError(url, own string, resp *http.Response) error {
	if own != "" {
		return statusError("GET", own, resp)
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("hass: GET %s: %s", withoutLogin(url), resp.Status)
	}
	return nil
}

// withoutLogin is an address as an error or a log line may repeat it: no user:pass@, no query and no
// fragment, since all three can be a secret (a camera's or a NAS's login, a signed link's token).
// Something that does not parse as an address is cut at its first ? or #, and a login in what is left
// is taken off by hand.
func withoutLogin(raw string) string {
	if u, err := neturl.Parse(raw); err == nil && u.Host != "" {
		u.User, u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = nil, "", false, "", ""
		return u.String()
	}
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	return loginInAddress.ReplaceAllString(raw, "//")
}

// loginInAddress is the user:pass@ of an address, from the // before it to the last @ before the path,
// since a password can hold an @.
var loginInAddress = regexp.MustCompile(`//[^/\s"']*@`)

// IsOwn is whether raw is an address on the configured Home Assistant, which is fetched as a path with
// the token (FetchURL) and so is not another host's to be judged as one.
func (c *Client) IsOwn(raw string) bool {
	_, ok := ownURL(c.baseURL(), raw)
	return ok
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

// Address is where Home Assistant is looked for, as the home_assistant action gave it, with no path:
// "http://192.168.1.20:8123". Empty when none was given. For saying where it could not be reached.
func (c *Client) Address() string {
	u, err := neturl.Parse(c.baseURL())
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// Token is the token itself (a sign-in's access token as it was last renewed), for taking it out of
// what is shared, never for showing; Secrets has a sign-in's refresh token too.
func (c *Client) Token() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.acc.Token
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

// MusicAssistantFor finds Music Assistant's own player for this device: the one it plays through the
// media player it owns here. own is this device's media player entity, and name is what this device calls
// itself - which is what it announces to Music Assistant over Sendspin, and so what Music Assistant names
// its player. Empty when there is none.
//
// Two ways, because what Music Assistant puts in active_queue changed: it used to be the entity id of this
// device's own player, and it is now the queue's own id (up<id>, which is the entity's unique_id as
// well) - so that test alone finds nothing, on every device running it. The name is what holds, and
// mass_player_type tells a player from a queue when both are named for the device.
func (c *Client) MusicAssistantFor(own, name string) (string, error) {
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
	// own is skipped in the search, so a device's own player named for itself is not taken for Music
	// Assistant's. But where Home Assistant gave the clean entity id to Music Assistant's player and the
	// device's own ESPHome entity a "_speaker" one, own is that Music Assistant player; skipping it alone
	// left nothing (#485), so it stands as the last resort when it is a Music Assistant player of this name.
	named, ownMatch := "", ""
	for _, s := range states {
		if !strings.HasPrefix(s.EntityID, "media_player.") {
			continue
		}
		if app, _ := s.Attributes["app_id"].(string); app != "music_assistant" {
			continue
		}
		isOwn := s.EntityID == own
		if !isOwn {
			if queue, _ := s.Attributes["active_queue"].(string); queue == own {
				return s.EntityID, nil
			}
		}
		if name == "" {
			continue
		}
		friendly, _ := s.Attributes["friendly_name"].(string)
		if !strings.EqualFold(strings.TrimSpace(friendly), strings.TrimSpace(name)) {
			continue
		}
		// A queue named for the device as well is not the thing to resume or unjoin, so it is not the
		// answer of last resort either: only a player is, whatever the server calls it.
		kind, _ := s.Attributes["mass_player_type"].(string)
		if kind == "queue" {
			continue
		}
		if isOwn {
			if kind == "player" && ownMatch == "" {
				ownMatch = s.EntityID
			}
			continue
		}
		if kind == "player" {
			return s.EntityID, nil
		}
		named = s.EntityID
	}
	if named != "" {
		return named, nil
	}
	return ownMatch, nil
}

// RemoveOwnEntry deletes Home Assistant's ESPHome entry for the device whose Wi-Fi address is mac
// (aa:bb:cc:dd:ee:ff, as layout.FactoryMAC gives it), for a reset that hands the device to someone else:
// left there, it holds the old key and fails until somebody deletes it. It reports how many it deleted.
// The token has to be an administrator's to delete one.
func (c *Client) RemoveOwnEntry(ctx context.Context, mac string) (int, error) {
	if !macForm.MatchString(mac) {
		return 0, errors.New("hass: not a MAC address")
	}
	// The device registry is not on the REST API, but its templates can read it: the config entries of the
	// ESPHome devices with this address among their connections.
	tmpl := "{% set ns = namespace(ids=[]) %}{% for e in integration_entities('esphome') %}{% set d = device_id(e) %}" +
		"{% if d and ('mac', '" + mac + "') in device_attr(d, 'connections') %}" +
		"{% set ns.ids = ns.ids + (device_attr(d, 'config_entries') | list) %}{% endif %}{% endfor %}" +
		"{{ ns.ids | unique | join(',') }}"
	out, err := c.doContext(ctx, "POST", "/api/template", map[string]any{"template": tmpl})
	if err != nil {
		return 0, err
	}
	// Only an ESPHome entry: the same device can sit in another integration's entry as well.
	b, err := c.doContext(ctx, "GET", "/api/config/config_entries/entry?domain=esphome", nil)
	if err != nil {
		return 0, err
	}
	var entries []struct {
		ID     string `json:"entry_id"`
		Domain string `json:"domain"`
	}
	if err := json.Unmarshal(b, &entries); err != nil {
		return 0, err
	}
	esphome := map[string]bool{}
	for _, e := range entries {
		if e.Domain == "esphome" {
			esphome[e.ID] = true
		}
	}
	n := 0
	for _, id := range strings.Split(strings.TrimSpace(string(out)), ",") {
		if !esphome[id] {
			continue
		}
		if _, err := c.doContext(ctx, "DELETE", "/api/config/config_entries/entry/"+neturl.PathEscape(id), nil); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

var macForm = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)

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

// CallResponse calls a service that answers, such as Music Assistant's library, and returns what it
// answered.
func (c *Client) CallResponse(domain, service string, data map[string]any) (map[string]any, error) {
	b, err := c.do("POST", "/api/services/"+domain+"/"+service+"?return_response", data)
	if err != nil {
		return nil, err
	}
	var out struct {
		Response map[string]any `json:"service_response"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("hass: %s.%s answered: %w", domain, service, err)
	}
	return out.Response, nil
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

// NewTestClient is a client for a fake Home Assistant, for tests of the packages that ask it things.
func NewTestClient(url, token string, h *http.Client) *Client {
	return &Client{acc: access{URL: url, Token: token}, http: h}
}
