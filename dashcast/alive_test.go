package main

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// A device is told the server is there, again and again, with nothing else to say; the session ending
// ends it; and a device that takes nothing ends the session.
func TestKeepAlive(t *testing.T) {
	was := aliveEvery
	aliveEvery = 20 * time.Millisecond
	t.Cleanup(func() { aliveEvery = was })

	device, server := net.Pipe()
	defer device.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		keepAlive(ctx, &sender{c: server}, func() { t.Error("a device that was reading was called lost") })
		close(done)
	}()
	for i := 0; i < 3; i++ {
		_ = device.SetReadDeadline(time.Now().Add(5 * time.Second))
		var msg [5]byte
		if _, err := io.ReadFull(device, msg[:]); err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		if n := binary.BigEndian.Uint32(msg[:4]); n != 1 || msg[4] != kindAlive {
			t.Fatalf("message %d is %d long of kind %d, want 1 of kind %d", i, n, msg[4], kindAlive)
		}
	}
	cancel()
	// One may be on its way out as the session ends: it is taken, so the write does not wait on it.
	_ = device.SetReadDeadline(time.Time{})
	go func() { _, _ = io.Copy(io.Discard, device) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the session ended and it goes on")
	}

	gone, server := net.Pipe()
	gone.Close()
	lost := make(chan struct{})
	go keepAlive(context.Background(), &sender{c: server}, func() { close(lost) })
	select {
	case <-lost:
	case <-time.After(5 * time.Second):
		t.Fatal("a device that is gone did not end the session")
	}
}
