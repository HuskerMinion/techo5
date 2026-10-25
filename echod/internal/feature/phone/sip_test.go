package phone

import (
	"context"
	"errors"
	"net"
	"testing"
)

// A call is rung for only when it came from the provider: the listener is on the home network, and on
// a panel nothing in front of it keeps anyone else's INVITE out.
func TestOnlyTheProviderRings(t *testing.T) {
	for _, c := range []struct {
		server, source string
		want           bool
	}{
		{"192.0.2.10", "192.0.2.10:5060", true},
		{"192.0.2.10", "192.0.2.10:40123", true},
		{"192.0.2.10", "[::ffff:192.0.2.10]:5061", true},
		{"192.0.2.10", "198.51.100.7:5060", false},
		{"192.0.2.10", "203.0.113.20:5060", false},
		{"2001:db8::5", "[2001:db8::5]:5061", true},
		{"2001:db8::5", "[2001:db8::6]:5061", false},
		{"localhost", "127.0.0.1:5061", true},
		{"localhost", "192.0.2.10:5061", false},
		{"192.0.2.10", "", false},
		{"192.0.2.10", "192.0.2.10", false},
		{"192.0.2.10", "provider.example:5060", false},
	} {
		if got := fromProvider(c.server, c.source); got != c.want {
			t.Errorf("a call from %q with the provider at %q: rung %v, want %v", c.source, c.server, got, c.want)
		}
	}
}

// The provider's addresses are remembered from each lookup, and a call is checked against them when
// the lookup fails: a name server that is down when the call arrives does not refuse the provider's
// call.
func TestTheProviderIsRememberedWhenTheLookupFails(t *testing.T) {
	p := newProvider("sip.provider.example")
	answer := []net.IPAddr{{IP: net.ParseIP("192.0.2.10")}}
	var down error
	p.lookup = func(context.Context, string) ([]net.IPAddr, error) { return answer, down }

	if !p.from("192.0.2.10:5061") {
		t.Fatal("a call from the address the lookup gave was refused")
	}
	answer = []net.IPAddr{{IP: net.ParseIP("192.0.2.11")}}
	if !p.from("192.0.2.10:5061") {
		t.Error("the provider moved, and a call from where it was looked up before was refused")
	}

	answer, down = nil, errors.New("no name server")
	for _, c := range []struct {
		source string
		want   bool
	}{
		{"192.0.2.10:5061", true},
		{"192.0.2.11:5060", true},
		{"192.0.2.99:5061", false},
	} {
		if got := p.from(c.source); got != c.want {
			t.Errorf("with the lookup down, a call from %s: rung %v, want %v", c.source, got, c.want)
		}
	}
}
