package api

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
)

// The connections open are counted as they are accepted and closed, a connection closed twice once, and
// an action called with none open tells the caller nobody took it.
func TestAnActionWithNobodyConnectedIsUnheard(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var n atomic.Int32
	cl := countListener{ln, &n}
	dial, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer dial.Close()
	c, err := cl.Accept()
	if err != nil {
		t.Fatal(err)
	}
	if n.Load() != 1 {
		t.Fatalf("%d open after one was accepted", n.Load())
	}
	_ = c.Close()
	_ = c.Close()
	if n.Load() != 0 {
		t.Fatalf("%d open after it was closed twice", n.Load())
	}

	a := &API{}
	unheard := 0
	call := component.Call{Service: "script.example", Unheard: func() { unheard++ }}
	a.call(call)
	a.srv.Store(a.serverFor(nil))
	a.call(call)
	if unheard != 2 {
		t.Errorf("%d of two actions with nobody connected, served or not, were unheard", unheard)
	}
	a.clients.Add(1)
	a.call(call)
	if unheard != 2 {
		t.Error("an action with Home Assistant connected was unheard")
	}
}

// counted is a connection on the count, as Accept hands it out, with the client's end of it.
func counted(n *atomic.Int32) (*countConn, net.Conn) {
	server, client := net.Pipe()
	n.Add(1)
	return &countConn{Conn: server, n: n}, client
}

// A connection the client hung up on comes off the count at the first read that finds it gone, though
// nothing closes it (the adopting server's look at a hello that never came); closing it later takes
// nothing more off.
func TestAConnectionThatDiesUnclosedIsUncountedOnRead(t *testing.T) {
	var n atomic.Int32
	c, client := counted(&n)
	_ = client.Close()
	if _, err := c.Read(make([]byte, 3)); err == nil {
		t.Fatal("read from a connection hung up on")
	}
	if n.Load() != 0 {
		t.Fatalf("%d open after the client hung up", n.Load())
	}
	_ = c.Close()
	_ = c.Close()
	if n.Load() != 0 {
		t.Errorf("%d open after it was closed too", n.Load())
	}
}

// Likewise at the first write that finds it gone, and a read failing after it takes nothing more off.
func TestAConnectionThatDiesUnclosedIsUncountedOnWrite(t *testing.T) {
	var n atomic.Int32
	c, client := counted(&n)
	_ = client.Close()
	if _, err := c.Write([]byte{0x01}); err == nil {
		t.Fatal("wrote to a connection hung up on")
	}
	if n.Load() != 0 {
		t.Fatalf("%d open after the client hung up", n.Load())
	}
	_, _ = c.Read(make([]byte, 3))
	_ = c.Close()
	if n.Load() != 0 {
		t.Errorf("%d open after a read and a close too", n.Load())
	}
}

// A read or a write that runs out of time leaves the connection on the count: it is still open.
func TestADeadlineLeavesTheConnectionCounted(t *testing.T) {
	var n atomic.Int32
	c, client := counted(&n)
	defer client.Close()
	_ = c.SetDeadline(time.Now().Add(-time.Second))
	if _, err := c.Read(make([]byte, 3)); err == nil || !timedOut(err) {
		t.Fatalf("read past its deadline: %v", err)
	}
	if _, err := c.Write([]byte{0x01}); err == nil || !timedOut(err) {
		t.Fatalf("write past its deadline: %v", err)
	}
	if n.Load() != 1 {
		t.Fatalf("%d open after a deadline ran out; want 1", n.Load())
	}
	_ = c.Close()
	if n.Load() != 0 {
		t.Errorf("%d open after it was closed", n.Load())
	}
}
