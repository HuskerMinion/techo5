//go:build !dot && !spot

package deck

import (
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/deckwire"
)

// fakeAgent answers like the agent on a computer, and keeps what it was asked.
type fakeAgent struct {
	mu   sync.Mutex
	got  []deckwire.Request
	addr string
}

func startAgent(t *testing.T, key string) *fakeAgent {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	a := &fakeAgent{addr: l.Addr().String()}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				conn, err := deckwire.Accept(c, key)
				if err != nil {
					return
				}
				for {
					var req deckwire.Request
					if conn.Receive(&req, 0) != nil {
						return
					}
					a.mu.Lock()
					a.got = append(a.got, req)
					a.mu.Unlock()
					reply := deckwire.Reply{OK: true}
					switch req.Op {
					case deckwire.OpHello:
						reply.Hello = &deckwire.Hello{Name: "Office PC", OS: "windows", Keys: true, Apps: []string{"OBS Studio"}, Scripts: []string{"Backup"}}
					case deckwire.OpRun:
						if req.Arg != "Backup" {
							reply = deckwire.Reply{Error: "no script called " + req.Arg}
						}
					}
					if conn.Send(reply) != nil {
						return
					}
				}
			}()
		}
	}()
	return a
}

const testKey = "ABCD-EFGH-IJKL-MNOP-QRST-UVWX-YZ23-4567"

func TestPairAndPress(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	a := startAgent(t, testKey)
	f := Get()

	if _, err := f.Pair(a.addr, "WRONG-KEYW-RONG-KEYW"); err == nil || !strings.Contains(err.Error(), "doesn't match") {
		t.Fatalf("a wrong key paired: %v", err)
	}
	if _, err := f.Pair(a.addr, "short"); err == nil {
		t.Fatal("a short key was tried")
	}
	name, err := f.Pair(a.addr, strings.ToLower(testKey))
	if err != nil || name != "Office PC" {
		t.Fatalf("pair: %q, %v", name, err)
	}
	if _, ok := config.Get().Deck.Computer("Office PC"); !ok {
		t.Fatal("not saved")
	}
	// A second computer that calls itself the same is refused, not swapped in for the first.
	const otherKey = "ZZZZ-YYYY-XXXX-WWWW-VVVV-UUUU-TTTT-SSSS"
	other := startAgent(t, otherKey)
	if _, err := f.Pair(other.addr, otherKey); err == nil || !strings.Contains(err.Error(), "already paired") {
		t.Errorf("a second Office PC: %v", err)
	}
	if c, _ := config.Get().Deck.Computer("Office PC"); c.Addr != a.addr {
		t.Error("the first Office PC was replaced")
	}
	// The same agent (the same key) at a new address is a re-pair, as after a new DHCP lease.
	moved := startAgent(t, testKey)
	if _, err := f.Pair(moved.addr, testKey); err != nil {
		t.Errorf("the same agent at a new address: %v", err)
	}
	if c, _ := config.Get().Deck.Computer("Office PC"); c.Addr != moved.addr {
		t.Error("the re-pair didn't take the new address")
	}
	if _, err := f.Pair(a.addr, testKey); err != nil {
		t.Errorf("pairing back: %v", err)
	}

	_ = config.Set().Deck().Page(0, []config.DeckButton{
		{Action: config.DeckPCKeys, Computer: "Office PC", Value: "ctrl+shift+m"},
		{Action: config.DeckPCRun, Computer: "Office PC", Value: "Not listed"},
		{Action: config.DeckPCType, Computer: "Laptop", Value: "hi"},
	})
	if err := f.Press(0, 0); err != nil {
		t.Fatalf("keys: %v", err)
	}
	if err := f.Press(0, 1); err == nil || !strings.Contains(err.Error(), "no script") {
		t.Fatalf("an unlisted script: %v", err)
	}
	if err := f.Press(0, 2); err == nil || !strings.Contains(err.Error(), "no computer") {
		t.Fatalf("an unpaired computer: %v", err)
	}
	a.mu.Lock()
	var keys bool
	for _, r := range a.got {
		keys = keys || (r.Op == deckwire.OpKeys && r.Arg == "ctrl+shift+m")
	}
	a.mu.Unlock()
	if !keys {
		t.Error("the agent never got the keys")
	}

	f.checkComputers()
	st := f.Computers()["Office PC"]
	if st.Hello == nil || len(st.Hello.Scripts) != 1 {
		t.Fatalf("state %+v", st)
	}
	b := config.DeckButton{Action: config.DeckPCKeys, Computer: "Office PC"}
	if lit, known := Lit(b, f.OBS(), f.Computers()); lit || !known {
		t.Errorf("a computer button on a computer that answers: lit %v known %v", lit, known)
	}
	if _, known := Lit(config.DeckButton{Action: config.DeckPCKeys, Computer: "Laptop"}, f.OBS(), f.Computers()); known {
		t.Error("a button for an unpaired computer counts as known")
	}
}
