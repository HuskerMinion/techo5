package panelctl

import (
	"bufio"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A line from the app reaches its kind's handler with the rest of its words; a kind nobody handles,
// and a blank line, are passed over.
func TestServeHandsEachLineToItsKind(t *testing.T) {
	got := make(chan []string, 4)
	mu.Lock()
	handlers["test-kind"] = func(args []string) { got <- args }
	mu.Unlock()
	t.Cleanup(func() { mu.Lock(); delete(handlers, "test-kind"); mu.Unlock() })

	app, dev := net.Pipe()
	done := make(chan struct{})
	go func() { serve(dev); close(done) }()
	_, _ = app.Write([]byte("nobody listens\n\ntest-kind can 2\n"))
	select {
	case args := <-got:
		if len(args) != 2 || args[0] != "can" || args[1] != "2" {
			t.Fatalf("handler got %q", args)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the line never reached its handler")
	}
	_ = app.Close()
	<-done
}

// Nothing dials the app before Ready, so an echod subcommand does not take its one connection; Ready
// dials once however often it is said, sends what was Sent before, and the app's lines then reach
// handlers registered before it.
func TestOnlyReadyDials(t *testing.T) {
	app, dev := net.Pipe()
	var dials atomic.Int32
	mu.Lock()
	dial = func() (net.Conn, error) {
		if dials.Add(1) > 1 {
			return nil, errors.New("one connection")
		}
		return dev, nil
	}
	mu.Unlock()
	t.Cleanup(func() {
		_ = app.Close()
		mu.Lock()
		dial = func() (net.Conn, error) { return nil, errors.New("test over") }
		delete(handlers, "early-kind")
		delete(latest, "status")
		mu.Unlock()
		start = sync.Once{} // a later run of the test dials again
	})
	got := make(chan []string, 1)
	Handle("early-kind", func(args []string) { got <- args })
	Send("status on")
	time.Sleep(100 * time.Millisecond)
	if dials.Load() != 0 {
		t.Fatal("dialed before Ready")
	}

	Ready()
	Ready()
	sc := bufio.NewScanner(app)
	if !sc.Scan() || sc.Text() != "status on" {
		t.Fatalf("the app was sent %q, want the line Sent before Ready", sc.Text())
	}
	_, _ = app.Write([]byte("early-kind 1\n"))
	select {
	case args := <-got:
		if len(args) != 1 || args[0] != "1" {
			t.Fatalf("handler got %q", args)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a line from the app never reached a handler registered before Ready")
	}
}
