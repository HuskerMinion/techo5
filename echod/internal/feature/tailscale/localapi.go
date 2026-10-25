//go:build !dot

package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// What this device asks its own tailscaled, over the unix socket tailscaled listens on: the same
// local API Tailscale's own command-line tool uses, spoken directly, so nothing else has to be
// shipped or run. Only the few calls this needs.

// status is the part of tailscaled's status this reads.
type status struct {
	BackendState string // NoState, NeedsLogin, NeedsMachineAuth, Stopped, Starting, Running
	HaveNodeKey  bool   // whether this device has joined a tailnet before
	Self         *node
}

// node is this device as the tailnet knows it.
type node struct {
	HostName     string
	DNSName      string
	TailscaleIPs []string
}

// localAPI talks to tailscaled at sock.
type localAPI struct {
	sock string
	http *http.Client
}

func newLocalAPI(sock string) *localAPI {
	return &localAPI{sock: sock, http: &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}},
	}}
}

// do sends one request; the host name is the one tailscaled requires of its local callers.
func (l *localAPI) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://local-tailscaled.sock/localapi/v0/"+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := l.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("tailscale: %s: %s: %s", path, resp.Status, bytes.TrimSpace(msg))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

// status is where the device stands on the tailnet, and the other nodes on it.
func (l *localAPI) status(ctx context.Context) (status, error) {
	var st status
	err := l.do(ctx, http.MethodGet, "status", nil, &st)
	return st, err
}

// login joins the tailnet with an auth key, under hostname. Only what this device needs is asked
// for: it does not take the tailnet's DNS (its own resolver stays as it is), does not route to other
// networks, and is nobody's exit node.
//
// As Tailscale's own "tailscale up" does it: start hands over the key and the settings, and a device
// that has never joined is then told to log in, which with a key given needs no browser.
func (l *localAPI) login(ctx context.Context, authKey, hostname string) error {
	before, err := l.status(ctx)
	if err != nil {
		return err
	}
	if err := l.do(ctx, http.MethodPost, "start", map[string]any{
		"AuthKey": authKey,
		"UpdatePrefs": map[string]any{
			"ControlURL":  "https://controlplane.tailscale.com",
			"WantRunning": true,
			"Hostname":    hostname,
			"CorpDNS":     false,
			"RouteAll":    false,
		},
	}, nil); err != nil {
		return err
	}
	// Joined before and still in: the new key and settings are all. Removed from the tailnet since
	// (it says it needs a login again), it logs in with the new key like a new device.
	if before.HaveNodeKey && before.BackendState != "NeedsLogin" {
		return nil
	}
	return l.do(ctx, http.MethodPost, "login-interactive", nil, nil)
}
