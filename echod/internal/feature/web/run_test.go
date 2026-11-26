package web

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// The port is held while nothing on it is switched on, so that no other program can take it and serve
// its own page there, and it is served as before once something is: closed, a connection gets nothing,
// and open, the page is there. Only the daemon stopping lets it go.
func TestThePortIsHeldWhileNothingIsOpen(t *testing.T) {
	var on atomic.Bool
	f := &Feature{poke: make(chan struct{}, 1), pages: []page{{
		path: "/page", open: on.Load,
		h: func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "served") },
	}}}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	var listens atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		_ = f.run(ctx, func() (net.Listener, error) {
			listens.Add(1)
			return ln, nil
		})
	}()
	t.Cleanup(func() {
		cancel()
		<-ended
	})

	held := func(when string) {
		t.Helper()
		if other, err := net.Listen("tcp", addr); err == nil {
			_ = other.Close()
			t.Fatalf("%s, another program could take the port", when)
		}
	}
	nothing := func(when string) {
		t.Helper()
		c, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			return // refused is nothing too
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = io.WriteString(c, "GET /page HTTP/1.1\r\nHost: x\r\n\r\n")
		if b, _ := io.ReadAll(c); len(b) != 0 {
			t.Fatalf("%s, the port answered %q", when, b)
		}
	}
	served := func() {
		t.Helper()
		client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			resp, err := client.Get("http://" + addr + "/page")
			if err == nil {
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK && string(b) == "served" {
					return
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("the page that was switched on was not served: %v", err)
			}
		}
	}
	poke := func() { f.poke <- struct{}{} }

	time.Sleep(50 * time.Millisecond) // Run has the port by now; nothing is open
	held("with nothing open")
	nothing("with nothing open")

	on.Store(true)
	poke()
	served()

	on.Store(false)
	poke()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		// Closing is asynchronous to the switch; wait for the port to stop answering.
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			break
		}
		_ = c.SetDeadline(time.Now().Add(time.Second))
		_, _ = io.WriteString(c, "GET /page HTTP/1.1\r\nHost: x\r\n\r\n")
		b, _ := io.ReadAll(c)
		c.Close()
		if len(b) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the port went on answering after everything was switched off")
		}
	}
	held("switched off again")
	nothing("switched off again")

	on.Store(true)
	poke()
	served()
	if n := listens.Load(); n != 1 {
		t.Errorf("the port was bound %d times, want once for the daemon's life", n)
	}

	cancel()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not end with its context")
	}
	other, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("the port was not let go when the daemon stopped: %v", err)
	}
	_ = other.Close()
}
