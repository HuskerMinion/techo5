package wifiwatch

import (
	"context"
	"testing"
	"time"
)

// Joined with no address for a minute and a half starts the DHCP client again, once, and again only
// after leaseWait; an address, or no Wi-Fi joined, starts the count over (the Echo Input's chip reset).
func TestJoinedWithNoAddressStartsTheDHCPClientAgain(t *testing.T) {
	now := time.Unix(1000, 0)
	joined, addr, restarted := true, true, 0
	w := &Watch{
		now:         func() time.Time { return now },
		network:     func(context.Context) (string, bool) { return "Home", joined },
		hasAddress:  func() bool { return addr },
		restartDHCP: func() { restarted++ },
		say:         func(string, ...any) {},
	}
	ctx := context.Background()
	step := func(d time.Duration, want int) {
		t.Helper()
		now = now.Add(d)
		w.lease(ctx)
		if restarted != want {
			t.Fatalf("at %v: restarted %d times, want %d", now.Sub(time.Unix(1000, 0)), restarted, want)
		}
	}
	step(0, 0) // joined with an address
	addr = false
	step(30*time.Second, 0) // the address goes: the count starts
	step(30*time.Second, 0)
	step(30*time.Second, 0) // 60 s without
	step(30*time.Second, 1) // 90 s without: started again
	step(30*time.Second, 1) // not again straight away
	step(4*time.Minute, 1)
	step(30*time.Second, 2) // leaseWait later, still none: again
	addr = true
	step(30*time.Second, 2) // an address: the count is over
	addr = false
	step(30*time.Second, 2)
	step(60*time.Second, 2) // a new count, and still within leaseWait of the last restart
	joined = false
	step(10*time.Minute, 2) // not joined: nothing to get a lease on
	joined = true
	step(30*time.Second, 2)
	step(90*time.Second, 3)
}
