package hass

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	neturl "net/url"
	"strings"
	"sync"
	"time"
)

// Signing in with Home Assistant (#567): the setup page sends the browser to Home Assistant's own
// login, the way its mobile apps sign in, and Home Assistant sends it back with a code that the
// device, not the browser, trades for a token. The token is the signed-in user's, listed in that
// user's profile under the device's address, and can be taken back there on its own. It is a short
// one, half an hour, with a refresh token the device renews it with; the long-lived token the
// home_assistant action gives still works as it always has.

// renewAhead is how long before the access token runs out it is renewed.
const renewAhead = 2 * time.Minute

// tokenAnswer is Home Assistant's /auth/token reply.
type tokenAnswer struct {
	Access    string  `json:"access_token"`
	Refresh   string  `json:"refresh_token"`
	ExpiresIn float64 `json:"expires_in"`
	Error     string  `json:"error"`
}

// ErrSignedOut is a refresh token Home Assistant no longer takes: revoked in the profile, or the user
// removed. The device needs connecting again.
var ErrSignedOut = errors.New("hass: Home Assistant took this device's sign-in back; connect it again")

// CheckAddress is a Home Assistant address as Set takes it, cleaned, or an error saying it is not one.
func CheckAddress(raw string) (string, error) {
	u, err := neturl.Parse(cleanURL(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("hass: the address is not one like http://homeassistant.local:8123")
	}
	return normalURL(u), nil
}

// AuthorizeURL is where the browser goes to sign in: Home Assistant's login, which comes back to
// redirect with a code and state.
func AuthorizeURL(base, clientID, redirect, state string) string {
	q := neturl.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirect)
	q.Set("state", state)
	return base + "/auth/authorize?" + q.Encode()
}

// SignIn trades the code Home Assistant sent the browser back with for tokens, and keeps them in
// place of whatever access the device had. A sign-in it replaces is taken back at Home Assistant.
func (c *Client) SignIn(ctx context.Context, base, clientID, code string) error {
	base, err := CheckAddress(base)
	if err != nil {
		return err
	}
	if code == "" || clientID == "" {
		return errors.New("hass: the sign-in came back without a code")
	}
	t, err := c.askToken(ctx, base, neturl.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID},
	})
	if err != nil {
		return err
	}
	if t.Refresh == "" {
		return errors.New("hass: Home Assistant gave no refresh token")
	}
	c.renew.Lock()
	defer c.renew.Unlock()
	c.mu.Lock()
	was := c.acc
	c.acc = access{URL: base, Token: t.Access, Refresh: t.Refresh, ClientID: clientID, Expires: expiry(t)}
	c.signedOut = false
	c.mu.Unlock()
	if err := c.save(); err != nil {
		return err
	}
	c.revokeLater(was)
	return nil
}

// SignedIn is whether the access is a sign-in (renewed by the device) rather than a long-lived token.
func (c *Client) SignedIn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.acc.Refresh != ""
}

// SignedOut is whether Home Assistant took the device's sign-in back, since it last signed in.
func (c *Client) SignedOut() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.signedOut
}

// Disconnect forgets the access, taking a sign-in back at Home Assistant first so it leaves the
// user's profile too. The address stays, for connecting again.
func (c *Client) Disconnect(ctx context.Context) error {
	c.renew.Lock()
	defer c.renew.Unlock()
	c.mu.Lock()
	was := c.acc
	c.acc = access{URL: was.URL}
	c.signedOut = false
	c.mu.Unlock()
	if was.Refresh != "" {
		if err := c.revoke(ctx, was); err != nil {
			slog.Warn("hass: the sign-in could not be taken back at Home Assistant; remove it in the profile", "err", err)
		}
	}
	return c.save()
}

// Close is the access being left (api's leave, a reset): what is in memory still serves until the
// restart, for deleting the device's entry, but nothing is written to the file any more, so a renewal
// in the meantime cannot put back what leaving removed.
func (c *Client) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
}

// Revoke takes a sign-in back at Home Assistant and keeps the rest, for leaving it (api's leave, a
// reset), where the file is removed anyway; it closes the access too (Close). A long-lived token is not
// the device's to take back.
func (c *Client) Revoke(ctx context.Context) {
	c.Close()
	c.mu.Lock()
	acc := c.acc
	c.mu.Unlock()
	if acc.Refresh == "" {
		return
	}
	if err := c.revoke(ctx, acc); err != nil {
		slog.Warn("hass: the sign-in could not be taken back at Home Assistant; remove it in the profile", "err", err)
	}
}

// revokeLater takes back an access that was replaced, when it was a sign-in, without holding up the
// one that replaced it.
func (c *Client) revokeLater(was access) {
	if was.Refresh == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := c.revoke(ctx, was); err != nil {
			slog.Warn("hass: the earlier sign-in could not be taken back at Home Assistant; remove it in the profile", "err", err)
		}
	}()
}

func (c *Client) revoke(ctx context.Context, acc access) error {
	req, err := http.NewRequestWithContext(ctx, "POST", acc.URL+"/auth/revoke",
		strings.NewReader(neturl.Values{"token": {acc.Refresh}}.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return quiet(err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("hass: revoke: %s", resp.Status)
	}
	return nil
}

// token is the access token to send: a long-lived one as it is, a sign-in's renewed first when it is
// about to run out (or has been refused, force). One renewal at a time; the callers waiting on it
// take the token it got.
func (c *Client) token(ctx context.Context, force bool) (access, error) {
	c.mu.Lock()
	acc := c.acc
	c.mu.Unlock()
	if acc.Refresh == "" || (!force && time.Until(acc.Expires) > renewAhead) {
		return acc, nil
	}
	c.renew.Lock()
	defer c.renew.Unlock()
	c.mu.Lock()
	now := c.acc
	c.mu.Unlock()
	if now.Refresh == "" || now.Refresh != acc.Refresh {
		return now, nil // replaced while waiting: a sign-in, a token or a disconnect
	}
	if now.Token != acc.Token || (!force && time.Until(now.Expires) > renewAhead) {
		return now, nil // renewed by the caller ahead of this one
	}
	t, err := c.askToken(ctx, now.URL, neturl.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {now.Refresh}, "client_id": {now.ClientID},
	})
	if errors.Is(err, errRefused) {
		c.mu.Lock()
		c.acc = access{URL: now.URL}
		c.signedOut = true
		c.mu.Unlock()
		_ = c.save()
		slog.Warn("hass: Home Assistant refused to renew this device's sign-in; connect it again on the setup page")
		return access{}, ErrSignedOut
	}
	if err != nil {
		// Not answered, or not a refusal: the token in hand may still have minutes left (the renewal
		// starts ahead of its end), and a hiccup at /auth/token alone need not fail what it is for.
		if !force && time.Until(now.Expires) > 0 {
			slog.Warn("hass: renewing the sign-in failed; using the token until it runs out", "err", err)
			return now, nil
		}
		return access{}, err
	}
	c.mu.Lock()
	if c.acc.Refresh == now.Refresh {
		c.acc.Token, c.acc.Expires = t.Access, expiry(t)
	}
	now = c.acc
	c.mu.Unlock()
	if err := c.save(); err != nil {
		slog.Warn("hass: saving the renewed sign-in failed; it serves until the next restart", "err", err)
	}
	return now, nil
}

// errRefused is /auth/token turning the grant down, rather than not answering.
var errRefused = errors.New("hass: the grant was refused")

// refusals are the OAuth errors /auth/token answers when the grant itself is no good.
var refusals = map[string]bool{
	"invalid_grant": true, "invalid_request": true, "invalid_client": true,
	"unauthorized_client": true, "access_denied": true,
}

// askToken posts form to base's /auth/token.
func (c *Client) askToken(ctx context.Context, base string, form neturl.Values) (tokenAnswer, error) {
	var t tokenAnswer
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/auth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return t, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return t, quiet(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return t, err
	}
	_ = json.Unmarshal(b, &t)
	if (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusForbidden ||
		resp.StatusCode == http.StatusUnauthorized) && refusals[t.Error] {
		// invalid_grant, invalid_request: the code or refresh token is not one Home Assistant takes.
		// Only Home Assistant's own word for it: a bare 401 or 403 is as likely a proxy in front of it
		// while it restarts, and taken as a refusal it would sign the device out for good.
		return t, fmt.Errorf("%w (%s %s)", errRefused, resp.Status, t.Error)
	}
	if resp.StatusCode/100 != 2 {
		return t, fmt.Errorf("hass: /auth/token: %s", resp.Status)
	}
	if t.Access == "" {
		return t, errors.New("hass: Home Assistant gave no access token")
	}
	return t, nil
}

// quiet is a transport error without the address it repeats (which a sign-in's form is not in, but a
// log line need not carry the address twice either).
func quiet(err error) error {
	var ue *neturl.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("hass: %w", ue.Err)
	}
	return err
}

// expiry is when an answer's access token runs out; Home Assistant's are half an hour.
func expiry(t tokenAnswer) time.Time {
	in := time.Duration(t.ExpiresIn * float64(time.Second))
	if in <= 0 {
		in = 30 * time.Minute
	}
	return time.Now().Add(in)
}

// Secrets is everything the access holds that must not be shared: the token, and a sign-in's refresh
// token (diag's bundle takes them out).
func (c *Client) Secrets() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return []string{c.acc.Token, c.acc.Refresh}
}

var seen struct {
	sync.Mutex
	host string
}

// Saw is the address a connection to the device's ESPHome link came from, kept so the setup page
// can suggest Home Assistant's address (port 8123 on the same host) before one was given. Only an
// address on the home network: the link is Home Assistant's, so that is where it is.
func Saw(addr net.Addr) {
	host, _, err := net.SplitHostPort(addr.String())
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsPrivate() {
		return
	}
	seen.Lock()
	seen.host = host
	seen.Unlock()
}

// Suggested is the Home Assistant address to offer when none is set: where the ESPHome link was last
// connected from, or Home Assistant's usual name.
func Suggested() string {
	seen.Lock()
	host := seen.host
	seen.Unlock()
	if host == "" {
		return "http://homeassistant.local:8123"
	}
	return "http://" + net.JoinHostPort(host, "8123")
}
