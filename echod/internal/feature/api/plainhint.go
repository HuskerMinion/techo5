package api

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Home Assistant first tries a device without encryption, and learns that it needs a key from what
// the device says back: a frame that starts with Noise's indicator byte (aioesphomeapi raises
// RequiresEncryptionAPIError on it, and Home Assistant asks for the key). The ESPHome library hangs
// up on a plaintext hello without a word, so Home Assistant saw only the connection close and told
// people to put an api section in their YAML. Added by address, without discovery saying it is
// encrypted, a device could not be added at all (techo5-spot#3).
//
// plainHint answers a plaintext hello the way ESPHome's own firmware does, with an explicit
// handshake rejection, before the library sees the hello and closes the connection as it did.

// Frame indicators: plaintext ESPHome API, and Noise.
const (
	plaintextIndicator = 0x00
	noiseIndicator     = 0x01
)

// plainReject is ESPHome's explicit handshake rejection: a Noise frame (indicator, 16-bit length)
// whose payload is a failure byte and the reason.
var plainReject = func() []byte {
	reason := "Bad indicator byte"
	payload := append([]byte{0x01}, reason...)
	return append([]byte{noiseIndicator, byte(len(payload) >> 8), byte(len(payload))}, payload...)
}()

// hintListener hands out connections that answer a plaintext hello with plainReject.
type hintListener struct{ net.Listener }

func (l hintListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &hintConn{Conn: c}, nil
}

// servedListener is ln as the server is given it: each connection counted (clients.go), and, when hint
// is set, a plaintext hello told the device needs a key. The hint is the outer of the two, so the count
// sees every byte as it is, and the hint's Close ends the sending side through the count's.
func servedListener(ln net.Listener, n *atomic.Int32, hint bool) net.Listener {
	var served net.Listener = countListener{ln, n}
	if hint {
		served = hintListener{served}
	}
	return served
}

// hintConn looks at the first byte the client sends, once.
type hintConn struct {
	net.Conn
	once     sync.Once
	rejected atomic.Bool // plainReject went out; Close may come from another goroutine
}

func (c *hintConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n == 0 {
		return n, err
	}
	c.once.Do(func() {
		if p[0] == plaintextIndicator {
			// Best effort, and short: the library is about to close the connection either way.
			_ = c.Conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
			if _, werr := c.Conn.Write(plainReject); werr == nil {
				c.rejected.Store(true)
			}
			_ = c.Conn.SetWriteDeadline(time.Time{})
		}
	})
	return n, err
}

// Close, after a rejection, ends the sending side first and takes whatever else the client sent:
// closing with unread bytes waiting resets the connection, and a reset can make the client's side
// drop the rejection before reading it. Bounded, so a client that keeps sending cannot hold it.
func (c *hintConn) Close() error {
	if c.rejected.Load() {
		// Asked of whatever the connection is: the count's wrapping (clients.go) is under this one,
		// and passes it on to the TCP connection.
		if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		_ = c.Conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
		_, _ = io.Copy(io.Discard, io.LimitReader(c.Conn, 64<<10))
	}
	return c.Conn.Close()
}
