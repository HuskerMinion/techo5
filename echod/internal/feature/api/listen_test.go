package api

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// A port the techo5 before still holds is waited for, not given up on: once it is free, the API listens.
func TestListenWaitsForAPortStillInUse(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := held.Addr().String()

	saved := listenTries
	listenTries.first, listenTries.longest = 10*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { listenTries = saved })

	tries := 0
	savedListen := listenTCP
	listenTCP = func(a string) (net.Listener, error) {
		if tries++; tries == 3 {
			_ = held.Close()
		}
		return savedListen(a)
	}
	t.Cleanup(func() { listenTCP = savedListen })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ln, err := listen(ctx, addr)
	if err != nil {
		t.Fatalf("listen = %v, want the port once it was free", err)
	}
	_ = ln.Close()
	if tries != 3 {
		t.Fatalf("%d tries, want 3", tries)
	}
}

// Waiting ends with the context, and a failure that is not a port in use is not waited on.
func TestListenGivesUpOnlyWhenItShould(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := listen(ctx, held.Addr().String()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("held port: err = %v, want the context's end", err)
	}

	began := time.Now()
	if _, err := listen(context.Background(), "127.0.0.1:not-a-port"); err == nil {
		t.Fatal("bad address: listened")
	}
	if time.Since(began) > time.Second {
		t.Fatal("bad address: waited on")
	}
}
