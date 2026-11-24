package peeruid

import (
	"net"
	"os"
	"testing"
	"time"
)

// The other end of a real loopback connection is found in the kernel's table, as this test's own user.
func TestTheOtherEndOfALoopbackConnection(t *testing.T) {
	for _, network := range []string{"tcp4", "tcp6"} {
		addr := map[string]string{"tcp4": "127.0.0.1:0", "tcp6": "[::1]:0"}[network]
		ln, err := net.Listen(network, addr)
		if err != nil {
			t.Logf("%s: %v", network, err)
			continue
		}
		c, err := net.Dial(network, ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		s, err := ln.Accept()
		if err != nil {
			t.Fatal(err)
		}
		uid, err := Of(s.RemoteAddr().String(), s.LocalAddr().String())
		if err != nil || uid != uint32(os.Getuid()) {
			t.Errorf("%s: %d, %v; want %d", network, uid, err, os.Getuid())
		}
		// Ours is not root's, and not an add-on's until Trusted says so.
		if Owner(s.RemoteAddr().String(), s.LocalAddr().String()) != (os.Getuid() == 0) {
			t.Errorf("%s: trusted as the owner", network)
		}
		Trusted = func(u uint32) bool { return u == uint32(os.Getuid()) }
		if !Owner(s.RemoteAddr().String(), s.LocalAddr().String()) {
			t.Errorf("%s: an add-on's user is not trusted", network)
		}
		Trusted = func(uint32) bool { return false }
		c.Close()
		s.Close()
		ln.Close()
	}
}

func TestKey(t *testing.T) {
	if k, _ := key("127.0.0.1:8181"); k != "0100007F:1FF5" {
		t.Errorf("IPv4: %s", k)
	}
	if k, _ := key("[::1]:8181"); k != "00000000000000000000000001000000:1FF5" {
		t.Errorf("IPv6: %s", k)
	}
	if Owner("192.0.2.4:1234", "192.0.2.10:8181") {
		t.Error("a controller on the network was trusted")
	}
}

// A program that sends its request and hangs up at once leaves a closing socket, which the kernel lists
// as root's: it is not taken for root, or for anyone.
func TestAClosedConnectionIsNotRoots(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	c, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	s, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c.Close()
	time.Sleep(100 * time.Millisecond)
	if uid, err := Of(s.RemoteAddr().String(), s.LocalAddr().String()); err == nil {
		t.Errorf("a closed client read as user %d", uid)
	}
	if Owner(s.RemoteAddr().String(), s.LocalAddr().String()) {
		t.Error("a closed client was trusted")
	}
}
