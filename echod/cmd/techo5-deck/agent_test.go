package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/deckwire"
)

func TestReadScripts(t *testing.T) {
	p := filepath.Join(t.TempDir(), "scripts.txt")
	if err := ensureScriptsFile(p); err != nil {
		t.Fatal(err)
	}
	if s, _ := readScripts(p); len(s) != 0 {
		t.Fatalf("the example lines counted: %v", s)
	}
	body := string([]byte{0xef, 0xbb, 0xbf}) + "# note\nBackup = robocopy a b /MIR\nnoequals\n = nothing\nEmpty =\nBackup = second\nLights=lights.exe --x=1\n"
	os.WriteFile(p, []byte(body), 0o600)
	s, err := readScripts(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 2 || s[0] != (script{"Backup", "robocopy a b /MIR"}) || s[1] != (script{"Lights", "lights.exe --x=1"}) {
		t.Errorf("scripts %v", s)
	}
}

func TestOnlyListedScriptsRun(t *testing.T) {
	p := filepath.Join(t.TempDir(), "scripts.txt")
	os.WriteFile(p, []byte("Touch = touch "+filepath.Join(t.TempDir(), "x")+"\n"), 0o600)
	a := &agent{name: "pc", scriptsPath: p, keys: make(chan struct{}, 1)}
	a.refresh()
	if r := a.do(deckwire.Request{Op: deckwire.OpRun, Arg: "rm -rf /"}); r.OK {
		t.Fatal("a command not on the list ran")
	}
	if r := a.do(deckwire.Request{Op: deckwire.OpRun, Arg: "Touch"}); !r.OK {
		t.Fatalf("a listed script: %s", r.Error)
	}
	if r := a.do(deckwire.Request{Op: deckwire.OpOpen, Arg: "file:///etc/passwd"}); r.OK {
		t.Fatal("a file address opened")
	}
	if r := a.do(deckwire.Request{Op: deckwire.OpOpen, Arg: "Not An App"}); r.OK || !strings.Contains(r.Error, "no app") {
		t.Fatalf("an unknown app: %+v", r)
	}
	if r := a.do(deckwire.Request{Op: deckwire.OpKeys, Arg: "ctrl+nope"}); r.OK {
		t.Fatal("an unknown key was pressed")
	}
	if r := a.do(deckwire.Request{Op: "format_c"}); r.OK {
		t.Fatal("an unknown request was done")
	}
	if r := a.do(deckwire.Request{Op: deckwire.OpHello}); !r.OK || r.Hello.Name != "pc" || len(r.Hello.Scripts) != 1 {
		t.Fatalf("hello %+v", r)
	}
}

func TestOnlyTheLocalNetwork(t *testing.T) {
	for addr, want := range map[string]bool{
		"192.168.1.5": true, "10.0.0.2": true, "172.20.1.1": true, "127.0.0.1": true, "fd00::5": true,
		"fe80::1": true, "8.8.8.8": false, "100.64.0.10": false, "2001:4860::8888": false,
	} {
		if got := local(&net.TCPAddr{IP: net.ParseIP(addr), Port: 1}); got != want {
			t.Errorf("%s local = %v, want %v", addr, got, want)
		}
	}
}

func TestKeyKeptAndRemade(t *testing.T) {
	p := filepath.Join(t.TempDir(), "key")
	k1, err := loadKey(p, false)
	if err != nil {
		t.Fatal(err)
	}
	if k2, _ := loadKey(p, false); k2 != k1 {
		t.Error("the key changed between runs")
	}
	if k3, _ := loadKey(p, true); k3 == k1 {
		t.Error("-new-key kept the old key")
	}
}

func TestWebAddresses(t *testing.T) {
	for s, want := range map[string]bool{
		"https://example.com":               true,
		"http://192.168.1.20:8123/lovelace": true,
		"HTTPS://Example.com/path?q=1":      true,
		"file:///etc/passwd":                false,
		"https://":                          false,
		"https://user:pw@example.com":       false,
		"https://exa mple.com":              false,
		"https://example.com/\x7f":          false,
		"https://example.com/\u202e":        false,
		"javascript:alert(1)":               false,
		"https://example.com\"&calc":        false,
	} {
		if got := isWebAddress(s); got != want {
			t.Errorf("isWebAddress(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestOnlyPrivateScriptLists(t *testing.T) {
	p := filepath.Join(t.TempDir(), "scripts.txt")
	os.WriteFile(p, []byte("Ok = true\n"), 0o600)
	a := &agent{name: "pc", scriptsPath: p, keys: make(chan struct{}, 1)}
	a.loadScripts(true)
	if len(a.hello().Scripts) != 1 {
		t.Fatal("a private list wasn't read")
	}
	// A line added later is there for the next press, without restarting.
	os.WriteFile(p, []byte("Ok = true\nMore = true\n"), 0o600)
	future := time.Now().Add(time.Minute)
	os.Chtimes(p, future, future)
	if r := a.do(deckwire.Request{Op: deckwire.OpHello}); len(r.Hello.Scripts) != 2 {
		t.Fatalf("an edited list wasn't read again: %v", r.Hello.Scripts)
	}
}

// A key press waits a moment for typing to finish, then gives up rather than landing minutes later.
func TestKeysWaitOnlyAMoment(t *testing.T) {
	a := &agent{keys: make(chan struct{}, 1)}
	if !a.takeKeys(time.Second) {
		t.Fatal("free keys weren't taken")
	}
	start := time.Now()
	if a.takeKeys(100 * time.Millisecond) {
		t.Fatal("held keys were taken twice")
	}
	if time.Since(start) > time.Second {
		t.Error("waited far too long")
	}
	a.giveKeys()
	if !a.takeKeys(time.Second) {
		t.Error("given-back keys weren't taken")
	}
}
