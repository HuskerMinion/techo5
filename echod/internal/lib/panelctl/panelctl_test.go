package panelctl

import (
	"net"
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

// Ready may be said more than once.
func TestReadyTwice(t *testing.T) {
	Ready()
	Ready()
	select {
	case <-ready:
	default:
		t.Fatal("not ready after Ready")
	}
}
