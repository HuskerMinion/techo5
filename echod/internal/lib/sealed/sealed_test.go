package sealed

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
)

func pair(t *testing.T, clientKey, serverKey []byte) (*Conn, *Conn, error, error) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	type res struct {
		c   *Conn
		err error
	}
	done := make(chan res, 1)
	go func() {
		c, err := Server(b, "test/1", serverKey)
		if err != nil {
			b.Close() // as a real server would, so the client's read ends
		}
		done <- res{c, err}
	}()
	c, cerr := Client(a, "test/1", clientKey)
	s := <-done
	return c, s.c, cerr, s.err
}

// The same key both ends: what one writes, the other reads, however large.
func TestSameKeyTalks(t *testing.T) {
	k := Key("test", "same word")
	c, s, cerr, serr := pair(t, k, k)
	if cerr != nil || serr != nil {
		t.Fatalf("handshake: client %v, server %v", cerr, serr)
	}
	msg := bytes.Repeat([]byte("intercom "), 20000) // more than one record
	go func() { _, _ = c.Write(msg) }()
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(s, got); err != nil || !bytes.Equal(got, msg) {
		t.Fatalf("read back %d bytes, err %v", len(got), err)
	}
}

// A different key fails the handshake on the server's side, before anything is said.
func TestWrongKeyIsRefused(t *testing.T) {
	_, _, _, serr := pair(t, Key("test", "one word"), Key("test", "another"))
	if !errors.Is(serr, ErrKey) {
		t.Fatalf("server took a wrong key: %v", serr)
	}
}

// One word under two labels is two keys.
func TestLabelsKeepKeysApart(t *testing.T) {
	if bytes.Equal(Key("a", "w"), Key("b", "w")) {
		t.Fatal("two labels gave the same key")
	}
}
