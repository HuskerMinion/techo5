package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// syncBuffer is output read while it is written.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuffer) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func waitFor(t *testing.T, out *syncBuffer, what string) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if strings.Contains(out.String(), what) {
			return
		}
	}
	t.Fatalf("waited for %q in:\n%s", what, out.String())
}

// An author's add-on, run against the fake: it says hello, answers a command typed at the keyboard,
// has its requests logged, and is refused what its manifest does not use.
func TestTheFakeServesAnAddon(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("the API refuses root")
	}
	dir, err := os.MkdirTemp("", "afake")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	man := filepath.Join(dir, "addon.json")
	if err := os.WriteFile(man, []byte(`{"id":"meals","version":"0.1.0","command":["meals"],"ports":[],"root":false,
	 "memory_mb":10,"api":1,"capabilities":["commands","timers"],"commands":[{"name":"next"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	in, keys := io.Pipe()
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- run(man, "", filepath.Join(dir, "run"), "show", nil, in, out) }()
	waitFor(t, out, "export TECHO5_ADDON_API=")
	path := regexp.MustCompile(`TECHO5_ADDON_API=(\S+)`).FindStringSubmatch(out.String())[1]

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := addonapi.Dial(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if w := c.Welcome(); w.Addon != "meals" || w.Device.Width != 960 {
		t.Errorf("welcome %+v", w)
	}
	go func() {
		for ev := range c.Events() {
			var ce addonapi.CommandEvent
			if ev.Event == addonapi.EventCommand && ev.Decode(&ce) == nil && ce.Call != 0 {
				_ = c.ReplyCommand(context.Background(), ce.Call, true, "Step two")
			}
		}
	}()
	if _, err := c.StartTimer(ctx, "rice", time.Minute); err != nil {
		t.Fatal(err)
	}
	waitFor(t, out, "request timer.start")
	if _, err := c.Speak(ctx, "hi"); addonapi.CodeOf(err) != addonapi.CodeDenied {
		t.Errorf("speak without the capability: %v", err)
	}
	if _, err := io.WriteString(keys, "command next\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, out, `Home Assistant is answered: "Step two"`)
	_, _ = io.WriteString(keys, "quit\n")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// A manifest the device would refuse is refused here too.
func TestTheFakeReadsTheManifestAsTheDeviceDoes(t *testing.T) {
	dir := t.TempDir()
	man := filepath.Join(dir, "addon.json")
	_ = os.WriteFile(man, []byte(`{"id":"meals","version":"0.1.0","command":["meals"],"ports":[],"root":false,
	 "memory_mb":10,"api":1,"capabilities":["commands"]}`), 0o644)
	if err := run(man, "", "", "show", nil, strings.NewReader(""), io.Discard); err == nil {
		t.Error("commands without any declared was taken")
	}
}
