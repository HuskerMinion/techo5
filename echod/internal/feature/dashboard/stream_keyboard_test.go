//go:build !dot

package dashboard

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flynn/noise"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// liveDashcast is a dashcast server that keeps its one connection: after the hello, send writes a
// framed message to the device and lines has every line the device sends.
func liveDashcast(t *testing.T, key string) (addr string, send chan<- []byte, lines <-chan map[string]any) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	out, in := make(chan []byte, 8), make(chan map[string]any, 32)
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		hs, err := noise.NewHandshakeState(noise.Config{
			CipherSuite: suite(), Random: rand.Reader, Pattern: noise.HandshakeNN, Initiator: false,
			Prologue: []byte(prologue), PresharedKey: psk(key), PresharedKeyPlacement: 0,
		})
		if err != nil {
			return
		}
		first, err := readFrame(c)
		if err != nil {
			return
		}
		if _, _, _, err := hs.ReadMessage(nil, first); err != nil {
			return
		}
		reply, fromDevice, toDevice, err := hs.WriteMessage(nil, nil)
		if err != nil || writeFrame(c, reply) != nil {
			return
		}
		sc := &secureConn{Conn: c, send: toDevice, recv: fromDevice}
		go func() {
			r := bufio.NewReader(sc)
			for {
				line, err := r.ReadBytes('\n')
				if err != nil {
					return
				}
				var m map[string]any
				_ = json.Unmarshal(line, &m)
				in <- m
			}
		}()
		for {
			select {
			case <-done:
				return
			case m := <-out:
				var hdr [4]byte
				binary.BigEndian.PutUint32(hdr[:], uint32(len(m)))
				if _, err := sc.Write(append(hdr[:], m...)); err != nil {
					return
				}
			}
		}
	}()
	return ln.Addr().String(), out, in
}

// A field the server says got focus reaches the page's view with its text, a new word each time; the
// typing goes back as text and keys; and the field is let go of when the connection goes.
func TestTheStreamedPagesFieldAndTyping(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	const key = "a-long-enough-test-key"
	addr, send, lines := liveDashcast(t, key)
	if err := config.Set().Dashboard().Server(addr, key); err != nil {
		t.Fatal(err)
	}
	f := &Feature{}
	f.Stream(480, 320)
	defer f.Close()
	next := func(what string) map[string]any {
		t.Helper()
		select {
		case m := <-lines:
			return m
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: nothing came", what)
		}
		return nil
	}
	if h := next("the hello"); h["w"] != 480.0 {
		t.Fatalf("hello %v", h)
	}
	view := func(what string, ok func(Field) bool) Field {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
			if fl := f.Stream(480, 320).Field; ok(fl) {
				return fl
			}
		}
		t.Fatalf("%s: the view has %+v", what, f.Stream(480, 320).Field)
		return Field{}
	}

	send <- append([]byte{kindKeyboard}, `{"up":true,"kind":"search","value":"Abba","label":"Search music"}`...)
	first := view("the field up", func(fl Field) bool { return fl.Up })
	if first.Kind != "search" || first.Value != "Abba" || first.Label != "Search music" || first.Seq == 0 {
		t.Errorf("the field %+v", first)
	}
	f.Type("x")
	f.Key("Backspace")
	f.Key("Enter")
	for _, want := range []map[string]any{{"t": "text", "s": "x"}, {"t": "key", "k": "Backspace"}, {"t": "key", "k": "Enter"}} {
		m := next("typing")
		if m["t"] != want["t"] || m["s"] != want["s"] || m["k"] != want["k"] {
			t.Errorf("sent %v, want %v", m, want)
		}
	}
	send <- append([]byte{kindKeyboard}, `{"up":false}`...)
	view("the field down", func(fl Field) bool { return !fl.Up && fl.Seq > first.Seq })

	send <- append([]byte{kindKeyboard}, `{"up":true,"kind":"text","value":"`+strings.Repeat("a", 2*fieldMax)+`"}`...)
	long := view("a long field up", func(fl Field) bool { return fl.Up })
	if len(long.Value) != fieldMax {
		t.Errorf("a long field's text is %d characters, want %d", len(long.Value), fieldMax)
	}

	f.Close()
	if fl := f.Stream(480, 320).Field; fl.Up {
		t.Errorf("a new connection starts with the last one's field up: %+v", fl)
	}
}
