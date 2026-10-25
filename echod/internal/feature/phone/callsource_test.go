//go:build !panel

package phone

import (
	"context"
	"net"
	"testing"
)

// An Echo rings for every call its listener is offered, whatever its source and whether or not the
// provider's name can be looked up: its firewall is what keeps strangers out.
func TestAnEchoNeverRefusesACallForItsSource(t *testing.T) {
	p := newProvider("sip.provider.example")
	p.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		t.Error("an Echo looked the provider up for a call")
		return nil, nil
	}
	for _, from := range []string{"192.0.2.10:5061", "198.51.100.7:5060", "", "provider.example:5060"} {
		if !p.admits(from) {
			t.Errorf("an Echo refused a call from %q", from)
		}
	}
}
