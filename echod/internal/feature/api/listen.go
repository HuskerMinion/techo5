package api

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"syscall"
	"time"
)

// listenTries paces the waits for a port still in use, starting short and growing to its longest.
var listenTries = struct{ first, longest time.Duration }{time.Second, 5 * time.Second}

// listenTCP is net.Listen, swapped out by tests.
var listenTCP = func(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }

// listen opens the port, waiting for as long as it is in use. A techo5 started again quickly finds the
// one before it still holding the port, and giving up then left the device without its API until the
// next restart: the service is not started again (it is built once, in Start), so the screen said
// "waiting for Home Assistant" for good. Any other failure is returned as it is.
func listen(ctx context.Context, addr string) (net.Listener, error) {
	wait := listenTries.first
	waited := false
	for {
		ln, err := listenTCP(addr)
		if err == nil {
			if waited {
				slog.Info("api: the port is free again", "addr", addr)
			}
			return ln, nil
		}
		if !errors.Is(err, syscall.EADDRINUSE) {
			return nil, err
		}
		if !waited {
			slog.Warn("api: the port is still in use, waiting for it", "addr", addr, "err", err)
			waited = true
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		wait = min(2*wait, listenTries.longest)
	}
}
