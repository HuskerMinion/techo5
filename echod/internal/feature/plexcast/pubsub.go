//go:build !dot

package plexcast

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The account's socket: how a casting app that is not on this network reaches the player. Plex's own
// clients hold one open, and what comes down it are the same Companion commands the LAN sends, keyed
// by their path in JSON rather than made into HTTP requests -- which is why they are turned back into
// requests against the same handlers (dispatch): one implementation of every command serves both.
//
// The address names the account and the device, and carries the token: a socket with neither is
// refused.

// cloud is the socket's life, with the reconnects a house's connection needs. A closed socket is
// ordinary -- a rebooted router, plex.tv moving a server -- so it is opened again, backing off to a
// minute so a device whose account is broken is not a busy loop.
func (f *Feature) cloud(ctx context.Context) {
	backoff := 2 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		err := f.socket(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Debug("plex: the account's socket closed", "err", err, "again in", backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

// socket is one connection from dial to close. The token and the account number are read at dial time,
// so a link that ended while the socket was up is not reconnected with what it had.
func (f *Feature) socket(ctx context.Context) error {
	p := config.Get().Plex
	if p.Token == "" || p.UserID == "" {
		// Without the account's number there is no address to dial. The LAN player works regardless,
		// so this waits for the next link rather than spinning.
		<-ctx.Done()
		return nil
	}
	u := pubsubURL + "/" + url.PathEscape(p.UserID) + "/" + url.PathEscape(clientIdentifier()) +
		"?X-Plex-Token=" + url.QueryEscape(p.Token)
	d := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	conn, _, err := d.DialContext(ctx, u, http.Header{"X-Plex-Client-Identifier": {clientIdentifier()}})
	if err != nil {
		return err
	}
	defer conn.Close()
	slog.Info("plex: the account's socket is open")
	// The socket is closed when the account goes away, so the read below ends rather than waiting for a
	// message that will never come.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	// Plex's sockets are quiet for long stretches and a phone's command is small: no read limit to
	// speak of, but a bounded one all the same.
	conn.SetReadLimit(1 << 20)
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		f.cloudMessage(msg)
	}
}

// cloudMessage is one frame: JSON keyed by the command's path, either {"player/playback/pause": {...}}
// or {"path": "player/playback/pause", "params": {...}}. Anything else is Plex talking about itself
// (a notification, a keepalive) and is left alone.
func (f *Feature) cloudMessage(msg []byte) {
	var frame map[string]json.RawMessage
	if err := json.Unmarshal(msg, &frame); err != nil {
		return
	}
	path := ""
	var params json.RawMessage
	if raw, ok := frame["path"]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			path = s
		}
		params = frame["params"]
	}
	if path == "" {
		for k, raw := range frame {
			if strings.HasPrefix(k, "player/") {
				path, params = k, raw
				break
			}
		}
	}
	if path == "" || !strings.HasPrefix(path, "player/") {
		return
	}
	f.dispatch(path, params)
}

// dispatch turns a cloud command into a request against the Companion server's own handlers. It is
// deliberately the only way a command is run: a second implementation for the cloud is how the two
// paths come to disagree about what a seek means.
func (f *Feature) dispatch(path string, params json.RawMessage) {
	q := url.Values{}
	var flat map[string]any
	if len(params) > 0 && json.Unmarshal(params, &flat) == nil {
		for k, v := range flat {
			if s, ok := v.(string); ok {
				q.Set(k, s)
				continue
			}
			// A number is as good as its text: Plex sends offsets and ids both ways.
			if b, err := json.Marshal(v); err == nil {
				q.Set(k, strings.Trim(string(b), `"`))
			}
		}
	}
	u := &url.URL{Path: "/" + strings.TrimPrefix(path, "/"), RawQuery: q.Encode()}
	r, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return
	}
	r.Host = "plex.tv"
	r.Header.Set("X-Plex-Client-Identifier", clientIdentifier())
	slog.Debug("plex: a command arrived from the account's socket", "path", path)
	f.handler().ServeHTTP(discard{}, r)
}

// discard is the answer to a command that came down the socket: there is nowhere to send one, and the
// socket is one way. The handlers still write as they do for a controller on the network.
type discard struct{}

func (discard) Header() http.Header         { return http.Header{} }
func (discard) Write(b []byte) (int, error) { return len(b), nil }
func (discard) WriteHeader(int)             {}
