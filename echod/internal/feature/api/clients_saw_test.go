package api

import (
	"net"
	"sync/atomic"
	"testing"
)

// The address goes to hass.Saw only once a session's worth has been sent, and only once.
func TestHomeAssistantsAddressOnlyAfterASession(t *testing.T) {
	var saw []net.Addr
	was := sawAddr
	sawAddr = func(a net.Addr) { saw = append(saw, a) }
	defer func() { sawAddr = was }()

	var n atomic.Int32
	c, peer := counted(&n)
	defer c.Close()
	go func() {
		buf := make([]byte, 64<<10)
		for {
			if _, err := peer.Read(buf); err != nil {
				return
			}
		}
	}()
	if _, err := c.Write(make([]byte, 300)); err != nil {
		t.Fatal(err)
	}
	if len(saw) != 0 {
		t.Fatal("a hello's worth was taken as Home Assistant's address")
	}
	for range 3 {
		if _, err := c.Write(make([]byte, sawAfter)); err != nil {
			t.Fatal(err)
		}
	}
	if len(saw) != 1 {
		t.Fatalf("saw %d times after a session's worth, want once", len(saw))
	}
}
