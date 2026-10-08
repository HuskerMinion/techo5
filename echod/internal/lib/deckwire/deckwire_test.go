package deckwire

import (
	"errors"
	"net"
	"strings"
	"testing"
)

// serve answers every request on l with handle, until l closes.
func serve(t *testing.T, key string, handle func(Request) Reply) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				conn, err := Accept(c, key)
				if err != nil {
					return
				}
				for {
					var req Request
					if conn.Receive(&req, 0) != nil {
						return
					}
					if conn.Send(handle(req)) != nil {
						return
					}
				}
			}()
		}
	}()
	return l.Addr().String()
}

func TestAskAndAnswer(t *testing.T) {
	addr := serve(t, "ABCD-EFGH", func(r Request) Reply {
		if r.Op == OpKeys && r.Arg == "ctrl+shift+m" {
			return Reply{OK: true}
		}
		return Reply{Error: "no such thing"}
	})
	// The key as typed on the other side: case, spaces and dashes don't matter.
	if _, err := Do(addr, "abcd efgh", Request{Op: OpKeys, Arg: "ctrl+shift+m"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Do(addr, "ABCDEFGH", Request{Op: OpRun, Arg: "x"}); err == nil || !strings.Contains(err.Error(), "no such thing") {
		t.Fatalf("a refusal came back as %v", err)
	}
}

func TestWrongKeyFailsTheHandshake(t *testing.T) {
	addr := serve(t, "RIGHT", func(Request) Reply { return Reply{OK: true} })
	if _, err := Do(addr, "WRONG", Request{Op: OpHello}); !errors.Is(err, ErrKey) {
		t.Fatalf("a wrong key: %v, want ErrKey", err)
	}
}

func TestNewKeyReadsWell(t *testing.T) {
	k, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if len(k) != 39 || strings.Count(k, "-") != 7 {
		t.Errorf("key %q is not 8 groups of 4", k)
	}
	if NormalizeKey(strings.ToLower(k)) != strings.ReplaceAll(k, "-", "") {
		t.Error("a lowercased key does not normalize back")
	}
	k2, _ := NewKey()
	if k == k2 {
		t.Error("two keys alike")
	}
}

func TestHelloRoundTrip(t *testing.T) {
	addr := serve(t, "K", func(r Request) Reply {
		return Reply{OK: true, Hello: &Hello{Name: "Office PC", OS: "windows", Keys: true, Scripts: []string{"Backup"}}}
	})
	r, err := Do(addr, "K", Request{Op: OpHello})
	if err != nil || r.Hello == nil || r.Hello.Name != "Office PC" || len(r.Hello.Scripts) != 1 {
		t.Fatalf("hello %+v, %v", r.Hello, err)
	}
}
