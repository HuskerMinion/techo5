//go:build sendspinlive

package sendspin

import (
	"context"
	"net"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestLive is the device's Sendspin client against a real server, run by hand: a server dials
// SS_PORT (default 8930) and the session runs for SS_SECS. SS_KEYS is the key store, kept between runs
// so that pairing can be checked across a reconnect. Not run in CI (build tag sendspinlive).
func TestLive(t *testing.T) {
	keysPath = os.Getenv("SS_KEYS")
	port := os.Getenv("SS_PORT")
	if port == "" {
		port = "8930"
	}
	secs, _ := strconv.Atoi(os.Getenv("SS_SECS"))
	if secs == 0 {
		secs = 60
	}
	if tok := PairingToken(); tok != "" {
		_ = os.WriteFile(keysPath+".token", []byte(tok), 0o600)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(secs)*time.Second)
	defer cancel()

	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		peer, _, _ := net.SplitHostPort(r.RemoteAddr)
		ws, plain, err := secured(ctx, conn, peer)
		if err != nil {
			t.Logf("handshake: %v", err)
			return
		}
		t.Logf("connected, plain=%v", plain)
		s := newSession(ws, nil, nil, "Live test", func(string) {})
		s.encrypted = !plain
		if err := s.client.Start(); err != nil {
			t.Logf("start: %v", err)
			return
		}
		_ = s.client.Send("client/state", s.stateFor(true, 5))
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.client.Done():
				t.Log("server closed the session")
				return
			case m := <-s.client.ControlMsgs:
				t.Logf("command %+v", m)
			case <-s.client.StreamStart:
			case <-s.client.StreamEnd:
			case <-s.client.StreamClear:
			case <-s.client.AudioChunks:
			case <-s.client.ArtworkChunks:
			case <-s.client.GroupUpdate:
			case <-s.client.ServerState:
			}
		}
	})
	srv := &http.Server{Addr: ":" + port, Handler: mux}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
