package api

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
)

// clients counts the connections open on the server now, for what wants to know whether anything it
// sends Home Assistant can arrive: an action called with no connection open goes nowhere, and says
// nothing (call). Counted where the connections are accepted, since the library keeps its own list to
// itself; one still shaking hands counts too, which is a moment, not a Home Assistant that is away.
type countListener struct {
	net.Listener
	n *atomic.Int32
}

func (l countListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.n.Add(1)
	return &countConn{Conn: c, n: l.n}, nil
}

// countConn takes itself off the count once, on whichever comes first: its first Close (the library
// may close a connection twice), or the first Read or Write that fails other than by running out of
// time. The library does not close every connection it is done with (one that hangs up while the
// adopting server looks at its hello is left as it is), and a scanner or a client gone without a word
// leaves a socket nobody closes until its next read fails; counted, any of those would keep an action
// called after it from ever being told it went unheard. A deadline that runs out is no such end: the
// connection is still there, and a read or a write after it can go on. Once, so the count goes down
// by one for each it went up by, and never below nothing.
type countConn struct {
	net.Conn
	n    *atomic.Int32
	once sync.Once
}

func (c *countConn) gone() { c.once.Do(func() { c.n.Add(-1) }) }

func (c *countConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err != nil && !timedOut(err) {
		c.gone()
	}
	return n, err
}

func (c *countConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if err != nil && !timedOut(err) {
		c.gone()
	}
	return n, err
}

func (c *countConn) Close() error {
	c.gone()
	return c.Conn.Close()
}

// CloseWrite ends the sending side of the connection under it, for hintConn's Close to find through
// the count (plainhint.go); one that has no such side says so.
func (c *countConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return errNoCloseWrite
}

var errNoCloseWrite = errors.New("api: the connection cannot end its sending side alone")

// timedOut is whether err is a deadline running out, which leaves the connection open.
func timedOut(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
