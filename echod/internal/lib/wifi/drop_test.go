package wifi

import (
	"context"
	"strings"
	"testing"
)

// Drop flushes the interface's IPv4 addresses and its global IPv6 ones, whatever the supplicant says.
func TestDropFlushesTheAddresses(t *testing.T) {
	var ran []string
	was, wasIface := ipCommand, iface
	t.Cleanup(func() { ipCommand, iface = was, wasIface })
	iface = func() string { return "wlan0" }
	ipCommand = func(_ context.Context, args ...string) (string, error) {
		ran = append(ran, strings.Join(args, " "))
		return "", nil
	}
	Drop(context.Background())
	want := []string{"-4 addr flush dev wlan0", "-6 addr flush dev wlan0 scope global"}
	if strings.Join(ran, "|") != strings.Join(want, "|") {
		t.Fatalf("ran %q, want %q", ran, want)
	}
}
