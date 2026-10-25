package localnames

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// fakeHosts points the feature at a hosts link of a test's own, as the root filesystem makes it.
func fakeHosts(t *testing.T) (file string) {
	t.Helper()
	dir := t.TempDir()
	file = filepath.Join(dir, "run-hosts")
	link := filepath.Join(dir, "etc-hosts")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	wasLink, wasFile, wasSoon := hostsLink, hostsFile, soon
	hostsLink, hostsFile, soon = link, file, 20*time.Millisecond
	t.Cleanup(func() { hostsLink, hostsFile, soon = wasLink, wasFile, wasSoon })
	return file
}

func fakeResolve(t *testing.T, f func(name string) (net.IP, error)) {
	t.Helper()
	was := resolve
	resolve = func(_ context.Context, name string) ([]net.IP, error) {
		ip, err := f(name)
		if err != nil {
			return nil, err
		}
		return []net.IP{ip}, nil
	}
	t.Cleanup(func() { resolve = was })
}

func waitFor(t *testing.T, file, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(file); err == nil && strings.Contains(string(b), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	b, _ := os.ReadFile(file)
	t.Fatalf("hosts file never had %q:\n%s", want, b)
}

// Home Assistant given as homeassistant.local: its address goes into the hosts file, and stays there
// while the name goes unanswered for a while.
func TestHomeAssistantsNameIsWritten(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	hass.Path = filepath.Join(t.TempDir(), "hass.json")
	if err := hass.Get().Set("http://HomeAssistant.local:8123", "token"); err != nil {
		t.Fatal(err)
	}
	file := fakeHosts(t)
	var calls, fail atomic.Int32
	fakeResolve(t, func(name string) (net.IP, error) {
		calls.Add(1)
		if name != "homeassistant.local" {
			t.Errorf("asked for %q", name)
		}
		if fail.Load() == 1 {
			return nil, errors.New("no answer")
		}
		return net.IPv4(192, 0, 2, 20), nil
	})
	// Every turn asks again, so the unanswered turns below come quickly.
	was := every
	every = 0
	t.Cleanup(func() { every = was })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { (&Feature{}).Run(ctx); close(done) }()
	waitFor(t, file, "192.0.2.20\thomeassistant.local")

	// Unanswered from now on, and asked again: the address stays.
	fail.Store(1)
	at := calls.Load()
	for calls.Load() < at+3 {
		time.Sleep(10 * time.Millisecond)
	}
	waitFor(t, file, "192.0.2.20\thomeassistant.local")
	cancel()
	<-done
}

// An address by number has nothing to look up, and a device whose hosts file is not the link writes
// nothing at all.
func TestNothingToDo(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	hass.Path = filepath.Join(t.TempDir(), "hass.json")
	if err := hass.Get().Set("http://192.0.2.20:8123", "token"); err != nil {
		t.Fatal(err)
	}
	if len(names()) != 0 {
		t.Errorf("names %v for an address by number", names())
	}

	dir := t.TempDir()
	wasLink, wasFile := hostsLink, hostsFile
	hostsLink, hostsFile = filepath.Join(dir, "plain-hosts"), filepath.Join(dir, "run-hosts")
	t.Cleanup(func() { hostsLink, hostsFile = wasLink, wasFile })
	if err := os.WriteFile(hostsLink, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeResolve(t, func(string) (net.IP, error) { t.Error("looked a name up"); return nil, nil })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	(&Feature{}).Run(ctx)
	if _, err := os.Stat(hostsFile); err == nil {
		t.Error("wrote a hosts file that is not this device's")
	}
}
