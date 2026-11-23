// Package panelctl is the panel's control line to its app: short text messages both ways, one a line,
// the first word saying what it is ("volume 12 30"). It carries what is neither sound nor pixels: the
// volume Android plays at, and later what the user does to the tablet itself.
//
// The app serves the abstract socket "techo5.ctl". This side keeps one connection, dialing again
// whenever it drops, and sends each kind of message's latest value again on every new connection, so
// an app that restarted is told where things stand.
package panelctl

import (
	"bufio"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/panelsock"
)

const socket = "@techo5.ctl"

var (
	mu       sync.Mutex
	conn     net.Conn
	latest   = map[string]string{} // first word -> the whole line, sent again on connect
	handlers = map[string]func(args []string){}
	start    sync.Once

	// ready closes once the daemon's main has begun (Ready), by when every package has registered its
	// handlers. Handle starts the line from a package's init, so the app's first lines can arrive while
	// other packages are still initializing; read then, a line whose handler is not registered yet was
	// dropped for good. On the panel, "android settings can" always lost that race and the Android
	// settings row never showed, and "apps begin" lost it on a slow start (techo5-panel#10).
	ready     = make(chan struct{})
	readyOnce sync.Once
)

// Ready says the daemon has started: every handler is registered, and lines from the app are handled
// from here on. Lines that came before wait for it rather than being dropped.
func Ready() { readyOnce.Do(func() { close(ready) }) }

// Send sends a line now if the app is there, and keeps it as the latest of its kind either way.
func Send(line string) {
	start.Do(run)
	kind, _, _ := strings.Cut(line, " ")
	mu.Lock()
	latest[kind] = line
	c := conn
	mu.Unlock()
	if c != nil {
		_, _ = c.Write([]byte(line + "\n"))
	}
}

// Tell sends a line now if the app is there, and keeps nothing: for a request worth making once, such
// as "update check", rather than a state a restarted app should be brought up to date with.
func Tell(line string) {
	start.Do(run)
	mu.Lock()
	c := conn
	mu.Unlock()
	if c != nil {
		_, _ = c.Write([]byte(line + "\n"))
	}
}

// Handle calls f with the rest of the words of every line from the app that starts with kind.
func Handle(kind string, f func(args []string)) {
	start.Do(run)
	mu.Lock()
	handlers[kind] = f
	mu.Unlock()
}

func run() {
	go func() {
		// refused is whether a socket that was not the app's has been logged since the last connection,
		// so one held by another app is said once rather than every second.
		refused := false
		for {
			c, err := panelsock.Dial(socket)
			if err != nil {
				if errors.Is(err, panelsock.ErrNotTheApp) && !refused {
					slog.Error("panel control line refused", "err", err)
					refused = true
				}
				time.Sleep(time.Second)
				continue
			}
			refused = false
			mu.Lock()
			conn = c
			for _, line := range latest {
				_, _ = c.Write([]byte(line + "\n"))
			}
			mu.Unlock()
			<-ready
			slog.Info("panel control line connected")
			serve(c)
			mu.Lock()
			conn = nil
			mu.Unlock()
			_ = c.Close()
			time.Sleep(time.Second)
		}
	}()
}

// serve hands each line from the app to its kind's handler until the connection ends.
func serve(c net.Conn) {
	sc := bufio.NewScanner(c)
	for sc.Scan() {
		words := strings.Fields(sc.Text())
		if len(words) == 0 {
			continue
		}
		mu.Lock()
		f := handlers[words[0]]
		mu.Unlock()
		if f != nil {
			f(words[1:])
		}
	}
}
