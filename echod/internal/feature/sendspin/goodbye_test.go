package sendspin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// The daemon stopping is a restart, which Music Assistant dials back; only the switch turned off is
// the user asking to go, which it does not.
func TestGoodbyeFor(t *testing.T) {
	daemon, stopDaemon := context.WithCancel(context.Background())
	running, stopRunning := context.WithCancelCause(daemon)
	session, stopSession := context.WithCancel(running)
	defer stopSession()
	stopDaemon()
	if got := goodbyeFor(session); got != "restart" {
		t.Errorf("the daemon stopping: %q, want restart", got)
	}
	stopRunning(nil)

	running, stopRunning = context.WithCancelCause(context.Background())
	session, stopSession = context.WithCancel(running)
	defer stopSession()
	stopRunning(errSwitchedOff)
	if got := goodbyeFor(session); got != "user_request" {
		t.Errorf("the switch turned off: %q, want user_request", got)
	}
}

// The switch turned off ends the sessions for good; the daemon stopping does not. The port is closed
// by a stand-in: the real one runs iptables.
func TestTheSwitchOffIsForGood(t *testing.T) {
	was := closePort
	closed := 0
	closePort = func(string) error { closed++; return nil }
	t.Cleanup(func() { closePort = was })

	for name, c := range map[string]struct {
		stop func(*Player)
		want string
	}{
		"the switch turned off": {(*Player).switchOff, "user_request"},
		"the daemon stopping":   {func(p *Player) { p.stop(nil) }, "restart"},
	} {
		closed = 0
		ctx, cancel := context.WithCancelCause(context.Background())
		p := &Player{state: &esphome.TextSensor{}, running: cancel}
		c.stop(p)
		if got := goodbyeFor(ctx); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
		if closed != 1 {
			t.Errorf("%s: the port was closed %d times, want once", name, closed)
		}
	}
}

// A running session that its context ends says goodbye for the reason it ended, read off a real
// socket as the server reads it: "restart" for the daemon stopping, "user_request" for the switch.
func TestARunningSessionSaysGoodbye(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))

	for _, c := range []struct {
		name  string
		cause error
		want  string
	}{
		{"the daemon stopping", nil, "restart"},
		{"the switch turned off", errSwitchedOff, "user_request"},
	} {
		t.Run(c.name, func(t *testing.T) {
			greeted := make(chan struct{})
			heard := make(chan string, 1)
			up := websocket.Upgrader{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := up.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				if _, _, err := conn.ReadMessage(); err != nil { // client/hello
					return
				}
				hello := `{"type":"server/hello","payload":{"server_id":"test","name":"Test",` +
					`"version":1,"active_roles":["player@v1"]}}`
				if conn.WriteMessage(websocket.TextMessage, []byte(hello)) != nil {
					return
				}
				close(greeted)
				for {
					_, b, err := conn.ReadMessage()
					if err != nil {
						return
					}
					var msg struct {
						Type    string `json:"type"`
						Payload struct {
							Reason string `json:"reason"`
						} `json:"payload"`
					}
					if json.Unmarshal(b, &msg) == nil && msg.Type == "client/goodbye" {
						heard <- msg.Payload.Reason
						return
					}
				}
			}))
			defer srv.Close()

			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()

			s := newSession(conn, newOut(speaker.New()), nil, "Test", func(string) {})
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			done := make(chan error, 1)
			go func() { done <- s.run(ctx) }()

			select {
			case <-greeted:
			case <-time.After(5 * time.Second):
				t.Fatal("the session never said hello")
			}
			cancel(c.cause)

			select {
			case reason := <-heard:
				if reason != c.want {
					t.Errorf("the server heard goodbye %q, want %q", reason, c.want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the server heard no goodbye")
			}
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("run: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the session did not end")
			}
		})
	}
}
