// Package wallclock keeps an Echo Wall Clock showing this device's timers.
//
// The clock is a 60-minute dial with one emphasised timer: the soonest one is drawn as a filled run
// from twelve, and every other running timer is a single bright segment at its minute. That is what a
// real Echo does with several timers, and it is what this package reproduces: one alert per timer,
// keyed by a token of its own, so adding or cancelling one leaves the others alone.
//
// The clock speaks the Alexa Gadgets protocol over Bluetooth Classic RFCOMM. It is a display and
// nothing else: Home Assistant, or this device's own screen, holds the timers, and a link that drops
// clears the clock until it is told again.
package wallclock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/agt"
	"github.com/HuskerMinion/techo5/echod/internal/lib/rfcomm"
)

// Sink is the write side used by the bridge; a disconnected link can return an error instead of
// blocking.
type Sink interface {
	Send(payload []byte) error
}

// conn is a live RFCOMM connection to a clock.
type conn struct {
	file *os.File

	writeMu sync.Mutex
	framer  *agt.Framer

	deframer agt.Deframer
	frames   chan agt.Frame

	done     chan struct{}
	closeOne sync.Once
	errMu    sync.Mutex
	err      error
}

// newConn wraps a connected stream socket and starts reading frames from it.
func newConn(file *os.File) *conn {
	c := &conn{
		file:   file,
		framer: agt.NewFramer(1),
		frames: make(chan agt.Frame, 32),
		done:   make(chan struct{}),
	}
	go c.readLoop()
	return c
}

// dial connects to the clock's data channel and completes the discover handshake.
func dial(ctx context.Context, mac string, connectTimeout, handshakeTimeout time.Duration) (*conn, error) {
	file, err := rfcomm.Dial(mac, agt.DefaultChannel, connectTimeout)
	if err != nil {
		return nil, err
	}
	c := newConn(file)
	if err := c.handshake(ctx, handshakeTimeout); err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}

// handshake sends Discover and waits for the clock's Discover.Response.
func (c *conn) handshake(ctx context.Context, timeout time.Duration) error {
	if err := c.Send(agt.Discover()); err != nil {
		return fmt.Errorf("send discover: %w", err)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case f := <-c.frames:
			if !f.ChecksumOK {
				continue
			}
			if msg := agt.DecodeMessage(f.Payload); msg.Name == "Discover.Response" {
				return nil
			}
		case <-c.done:
			return fmt.Errorf("link closed during handshake: %w", c.Err())
		case <-timer.C:
			return fmt.Errorf("timed out after %s waiting for Discover.Response", timeout)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Send frames a directive payload and writes it to the link.
func (c *conn) Send(payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if err := c.Err(); err != nil {
		return err
	}
	frame := c.framer.Frame(payload)
	for written := 0; written < len(frame); {
		n, err := c.file.Write(frame[written:])
		if err != nil {
			return fmt.Errorf("write frame: %w", err)
		}
		if n == 0 {
			return errors.New("write frame: short write")
		}
		written += n
	}
	return nil
}

// Frames delivers decoded frames from the clock.
func (c *conn) Frames() <-chan agt.Frame { return c.frames }

// Done is closed when the link fails or is closed.
func (c *conn) Done() <-chan struct{} { return c.done }

// Err reports why the link ended, if it did.
func (c *conn) Err() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.err
}

// close shuts the link down; safe to call more than once.
func (c *conn) close() error {
	c.fail(errors.New("closed"))
	return nil
}

func (c *conn) fail(err error) {
	c.closeOne.Do(func() {
		c.errMu.Lock()
		c.err = err
		c.errMu.Unlock()
		_ = c.file.Close()
		close(c.done)
	})
}

func (c *conn) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := c.file.Read(buf)
		if n > 0 {
			for _, f := range c.deframer.Feed(buf[:n]) {
				select {
				case c.frames <- f:
				case <-c.done:
					return
				}
			}
		}
		if err != nil {
			c.fail(fmt.Errorf("read from link: %w", err))
			return
		}
	}
}
