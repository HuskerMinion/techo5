package sendspin

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The device dialing Music Assistant itself, when Music Assistant is set up on it (config.
// MusicAssistant, the server the voice assistant asks for music).
//
// On the house's own network Music Assistant finds the device by mDNS and dials in (listen.go). A
// device farther away, reached over a VPN, can only be found from a list of addresses typed into
// Music Assistant, which it retries on its own schedule: after a stretch of the device being
// unreachable it can stop coming back at all, and the device then has music only in name. So the
// device also dials Music Assistant's Sendspin server, and keeps dialing, for as long as it has none.
//
// It shares the room with the listener: whichever connects first plays, and the other waits.

// maPort is Music Assistant's Sendspin server, on the host its API is on. A variable for the tests.
var maPort = 8927

const (
	// dialFirst and dialMost bound the wait between attempts: soon after a drop, then backing off to
	// a minute while Music Assistant is away.
	dialFirst = 5 * time.Second
	dialMost  = time.Minute

	// dialTimeout bounds one attempt, over a VPN that may be slow to wake.
	dialTimeout = 15 * time.Second
)

// maSendspinURL is Music Assistant's Sendspin server for its API address, or empty when Music
// Assistant is not set up or its address is not one to dial.
func maSendspinURL(api string) string {
	u, err := url.Parse(strings.TrimSpace(api))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return ""
	}
	scheme := "ws"
	if u.Scheme == "https" {
		scheme = "wss"
	}
	return (&url.URL{Scheme: scheme, Host: net.JoinHostPort(u.Hostname(), strconv.Itoa(maPort)), Path: path}).String()
}

// dialUnset is how often a device with no Music Assistant looks again, so that one set up on the
// setup page is dialed soon after.
const dialUnset = 15 * time.Second

// dial keeps a session with Music Assistant, dialed from here, until ctx ends.
func (l *listener) dial(ctx context.Context, name string) {
	wait := dialFirst
	for {
		target := ""
		if m := config.Get().MusicAssistant; m.Set() {
			target = maSendspinURL(m.URL)
		}
		switch {
		case target == "":
			wait = dialUnset
		case l.held():
			// A server already dialed in and holds the room.
			wait = dialFirst
		default:
			if l.dialOnce(ctx, target, name) {
				wait = dialFirst // a session that ran: try again soon after it ends
			} else {
				wait = min(wait*2, dialMost)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// held is whether a session holds the room now.
func (l *listener) held() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.busy
}

// runSession is a session on a connection made here; a variable so that a test can stand in for the
// player, which needs the speaker.
var runSession = func(ctx context.Context, l *listener, conn *websocket.Conn, name string) error {
	peer, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
	ws, plain, err := secured(ctx, conn, peer)
	if err != nil {
		return err
	}
	s := newSession(ws, l.out, l.bg, name, l.report)
	s.encrypted = !plain
	err = s.run(ctx)
	unstarted(plain, peer, err)
	return err
}

// dialOnce connects to Music Assistant and runs a session; it reports whether one ran for long enough to
// count, so that a server that takes the connection and drops it at once is backed off from like one
// that does not answer. The room is taken only once the connection is made: a server that dials in
// meanwhile is not turned away for a dial that may never answer.
func (l *listener) dialOnce(ctx context.Context, target, name string) (ran bool) {
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	conn, _, err := websocket.DefaultDialer.DialContext(dctx, target, nil)
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			slog.Debug("sendspin: music assistant not reached", "url", target, "err", err)
		}
		return false
	}
	defer conn.Close()
	if !l.take() {
		return true // a server dialed in while this connected: it has the room, and this one goes
	}
	defer l.give()

	slog.Info("sendspin connected to music assistant", "url", target)
	l.report(stateJoined)
	defer l.report(stateWaiting)

	start := time.Now()
	defer func() { ran = time.Since(start) > dialFirst*2 }()
	if err := runSession(ctx, l, conn, name); err != nil {
		slog.Warn("sendspin session with music assistant ended", "err", err)
	} else {
		slog.Info("sendspin music assistant disconnected")
	}
	return
}
