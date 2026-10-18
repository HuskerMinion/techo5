package sendspin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sendspin/sendspin-go/pkg/protocol"
	"github.com/gorilla/websocket"
	esphome "github.com/ygelfand/go-esphome-device"
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

// The switch turned off ends the sessions for good; the daemon stopping does not.
func TestTheSwitchOffIsForGood(t *testing.T) {
	for name, c := range map[string]struct {
		stop func(*Player)
		want string
	}{
		"the switch turned off": {(*Player).switchOff, "user_request"},
		"the daemon stopping":   {func(p *Player) { p.stop(nil) }, "restart"},
	} {
		ctx, cancel := context.WithCancelCause(context.Background())
		p := &Player{state: &esphome.TextSensor{}, running: cancel}
		c.stop(p)
		if got := goodbyeFor(ctx); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

// Leaving, a session says goodbye for its reason, read off a real socket as the server reads it.
func TestLeavingSaysGoodbye(t *testing.T) {
	got := make(chan string, 1)
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		var msg struct {
			Type    string `json:"type"`
			Payload struct {
				Reason string `json:"reason"`
			} `json:"payload"`
		}
		_, b, err := c.ReadMessage()
		if err == nil && json.Unmarshal(b, &msg) == nil && msg.Type == "client/goodbye" {
			got <- msg.Payload.Reason
		}
	}))
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	s := &session{client: protocol.NewClientFromConn(protocol.Config{}, conn)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.leave(ctx)

	select {
	case reason := <-got:
		if reason != "restart" {
			t.Errorf("the server heard goodbye %q, want restart", reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server heard no goodbye")
	}
}
