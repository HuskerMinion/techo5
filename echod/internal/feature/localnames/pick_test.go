package localnames

import (
	"context"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// ownIs makes 192.0.2.0/24 the device's own network for the test.
func ownIs(t *testing.T) {
	t.Helper()
	_, own, _ := net.ParseCIDR("192.0.2.0/24")
	was := onOwnNetwork
	onOwnNetwork = own.Contains
	t.Cleanup(func() { onOwnNetwork = was })
}

// The address known stays while it is answered; another takes its place when it is not, or when the
// known one is off the device's networks and an answer is on one.
func TestPick(t *testing.T) {
	ownIs(t)
	a, b, far := net.IPv4(192, 0, 2, 20), net.IPv4(192, 0, 2, 21), net.IPv4(198, 51, 100, 7)
	for _, c := range []struct {
		name  string
		known net.IP
		got   []net.IP
		want  net.IP
	}{
		{"first look", nil, []net.IP{a, far}, a},
		{"known and answered", b, []net.IP{a, b}, b},
		{"known, no longer answered", b, []net.IP{a}, a},
		{"known off the network, one on it now", far, []net.IP{a, far}, a},
		{"known off the network, still the only one", far, []net.IP{far}, far},
	} {
		if got := pick(c.known, c.got); !got.Equal(c.want) {
			t.Errorf("%s: picked %v, want %v", c.name, got, c.want)
		}
	}
}

// A second responder answering for the name now and then does not move the hosts file, and the name
// is written as it was given as well as in lowercase.
func TestAKnownAddressStays(t *testing.T) {
	ownIs(t)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	hass.Path = filepath.Join(t.TempDir(), "hass.json")
	if err := hass.Get().Set("http://HomeAssistant.local:8123", "token"); err != nil {
		t.Fatal(err)
	}
	file := fakeHosts(t)
	var calls atomic.Int32
	was := resolve
	resolve = func(_ context.Context, name string) ([]net.IP, error) {
		if calls.Add(1) == 1 {
			return []net.IP{net.IPv4(192, 0, 2, 20)}, nil
		}
		return []net.IP{net.IPv4(192, 0, 2, 99), net.IPv4(192, 0, 2, 20)}, nil
	}
	t.Cleanup(func() { resolve = was })
	wasEvery := every
	every = 0
	t.Cleanup(func() { every = wasEvery })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { (&Feature{}).Run(ctx); close(done) }()
	waitFor(t, file, "192.0.2.20\thomeassistant.local HomeAssistant.local\n")
	for calls.Load() < 4 {
		time.Sleep(10 * time.Millisecond)
	}
	waitFor(t, file, "192.0.2.20\thomeassistant.local HomeAssistant.local\n")
	cancel()
	<-done
}
