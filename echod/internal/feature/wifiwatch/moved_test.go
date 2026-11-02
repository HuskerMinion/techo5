package wifiwatch

import (
	"context"
	"testing"
)

// Going from one saved network to another by itself asks for a lease on the new one; the first network
// of the boot, the same one again, or no network at all do not (#384).
func TestMovingToAnotherNetworkRenewsTheLease(t *testing.T) {
	ssid, joined, renewed := "", false, 0
	w := &Watch{
		network: func(context.Context) (string, bool) { return ssid, joined },
		renew:   func() { renewed++ },
		say:     func(string, ...any) {},
	}
	ctx := context.Background()
	for _, step := range []struct {
		ssid   string
		joined bool
		want   int
	}{
		{"", false, 0},
		{"Old", true, 0},
		{"Old", true, 0},
		{"", false, 0},
		{"Main", true, 1},
		{"Main", true, 1},
		{"Old", true, 2},
	} {
		ssid, joined = step.ssid, step.joined
		w.moved(ctx)
		if renewed != step.want {
			t.Fatalf("on %q (joined %v): renewed %d times, want %d", step.ssid, step.joined, renewed, step.want)
		}
	}
}
