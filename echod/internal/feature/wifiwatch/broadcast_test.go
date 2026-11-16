package wifiwatch

import (
	"context"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// bcWorld is what the broadcast watcher sees, set by the test.
type bcWorld struct {
	now      time.Time
	g        wifi.Group
	up       bool
	held     bool
	reassocs int
	said     []string
}

func (bw *bcWorld) watch() *broadcast {
	return &broadcast{
		now:          func() time.Time { return bw.now },
		groupPackets: func() (wifi.Group, bool) { return bw.g, true },
		joined:       func(context.Context) bool { return bw.up },
		gatewayUp:    func(context.Context) bool { return bw.up },
		reassociate:  func(context.Context) error { bw.reassocs++; return nil },
		record:       func(context.Context, string) {},
		say:          func(msg string, _ ...any) { bw.said = append(bw.said, msg) },
		held:         func() bool { return bw.held },
		wait:         bcFirstWait,
	}
}

// step moves the clock on by one look; heard says whether the network broadcast in it. The device
// broadcasts now and then itself, which comes back to it and must not count.
func (bw *bcWorld) step(b *broadcast, heard bool) {
	bw.now = bw.now.Add(every)
	bw.g.BcastOut++
	bw.g.BcastIn++
	if heard {
		bw.g.BcastIn += 9
	}
	b.look(context.Background())
}

func newBcWorld() *bcWorld {
	return &bcWorld{now: time.Date(2026, 11, 15, 19, 44, 0, 0, time.UTC), up: true,
		g: wifi.Group{From: wifi.FromIPv4 | wifi.FromIPv6}}
}

// The case in techo5-dot#45: a busy network broadcasting every look, then a rekey after which none
// arrive. It reassociates once the quiet has lasted bcQuiet, and a reassociation that brought them back
// keeps the wait short, so the next rekey's loss is answered as soon.
func TestBroadcastsThatStopAreAnsweredByReassociating(t *testing.T) {
	bw := newBcWorld()
	b := bw.watch()
	for range 20 {
		bw.step(b, true)
	}
	for i := 0; i < int(bcQuiet/every)-1; i++ {
		bw.step(b, false)
	}
	if bw.reassocs != 0 {
		t.Fatalf("reassociated after %v of quiet, before bcQuiet", time.Duration(int(bcQuiet/every)-1)*every)
	}
	bw.step(b, false)
	if bw.reassocs != 1 {
		t.Fatalf("reassociations = %d after bcQuiet of quiet, want 1", bw.reassocs)
	}
	// Back at the next look: it was the reassociation, so the wait stays the first.
	bw.step(b, true)
	if b.wait != bcFirstWait || !b.tried.IsZero() {
		t.Fatalf("wait %v after the broadcasts came back at once, want %v", b.wait, bcFirstWait)
	}
	// Ten minutes of good reception, then the next rekey: answered again.
	for range 20 {
		bw.step(b, true)
	}
	for range int(bcQuiet / every) {
		bw.step(b, false)
	}
	if bw.reassocs != 2 {
		t.Fatalf("reassociations = %d after the next rekey's loss, want 2", bw.reassocs)
	}
}

// A reassociation that does not bring the broadcasts back is not repeated before the wait, and the
// wait doubles.
func TestAReassociationThatDoesNotHelpBacksOff(t *testing.T) {
	bw := newBcWorld()
	b := bw.watch()
	for range 20 {
		bw.step(b, true)
	}
	for range int(bcQuiet / every) {
		bw.step(b, false)
	}
	if bw.reassocs != 1 {
		t.Fatalf("reassociations = %d, want 1", bw.reassocs)
	}
	for range int(bcFirstWait/every) - 1 {
		bw.step(b, false)
	}
	if bw.reassocs != 1 {
		t.Fatalf("reassociated again within the wait: %d", bw.reassocs)
	}
	bw.step(b, false)
	if bw.reassocs != 2 || b.wait != 4*bcFirstWait {
		t.Fatalf("reassociations = %d, wait %v after the first wait; want 2 and %v", bw.reassocs, b.wait, 4*bcFirstWait)
	}
	said := false
	for _, s := range bw.said {
		said = said || s == "wifi: still no broadcasts after reassociating"
	}
	if !said {
		t.Error("a reassociation that did not help was not said")
	}
	// Coming back late, by itself, does not reset the wait.
	for range 10 {
		bw.step(b, false)
	}
	bw.step(b, true)
	if b.wait != 4*bcFirstWait {
		t.Errorf("wait %v after broadcasts came back by themselves, want it kept at %v", b.wait, 4*bcFirstWait)
	}
}

// A network that broadcasts only now and then, one never heard broadcasting, a link that is down, and
// a moment just after the other watcher reassociated are all left alone.
func TestQuietNetworksAndADownLinkAreLeftAlone(t *testing.T) {
	bw := newBcWorld()
	b := bw.watch()
	for range 3 * int(bcQuiet/every) {
		bw.step(b, false)
	}
	if bw.reassocs != 0 {
		t.Fatalf("reassociated on a network never heard broadcasting: %d", bw.reassocs)
	}

	// A broadcast every five minutes: never steady enough to be watched.
	for range 12 {
		bw.step(b, true)
		for range int(5*time.Minute/every) - 1 {
			bw.step(b, false)
		}
	}
	if bw.reassocs != 0 {
		t.Fatalf("reassociated on a network that broadcasts every five minutes: %d", bw.reassocs)
	}

	// Steady now, but the link is down when they stop.
	for range 20 {
		bw.step(b, true)
	}
	bw.up = false
	for range 2 * int(bcQuiet/every) {
		bw.step(b, false)
	}
	if bw.reassocs != 0 {
		t.Fatalf("reassociated a link that is down: %d", bw.reassocs)
	}

	// Up again, but the other watcher has just reassociated.
	bw.up, bw.held = true, true
	bw.step(b, false)
	if bw.reassocs != 0 {
		t.Fatalf("reassociated straight after the other watcher did: %d", bw.reassocs)
	}
	bw.held = false
	bw.step(b, false)
	if bw.reassocs != 1 {
		t.Fatalf("reassociations = %d once the link was up and nothing else had acted, want 1", bw.reassocs)
	}
}

// A network whose broadcasts come a minute and a half apart is given four times that before the quiet
// counts, not bcQuiet.
func TestTheQuietAllowedFollowsTheNetworksGaps(t *testing.T) {
	bw := newBcWorld()
	b := bw.watch()
	for range 12 {
		bw.step(b, true)
		for range 2 {
			bw.step(b, false)
		}
	}
	if b.longest != 90*time.Second {
		t.Fatalf("longest gap %v, want 1m30s", b.longest)
	}
	for range int(4*90*time.Second/every) - 3 {
		bw.step(b, false)
	}
	if bw.reassocs != 0 {
		t.Fatalf("reassociated before four times the longest gap: %d", bw.reassocs)
	}
	bw.step(b, false)
	if bw.reassocs != 1 {
		t.Fatalf("reassociations = %d after four times the longest gap, want 1", bw.reassocs)
	}
}

// Counters that went down, or a reading from other files, are a new start rather than a loss.
func TestBroadcastCountersThatJumpAreANewStart(t *testing.T) {
	bw := newBcWorld()
	b := bw.watch()
	for range 20 {
		bw.step(b, true)
	}
	bw.g.From = wifi.FromIPv6 // the IPv4 file missed: nothing to go by
	for range 2 * int(bcQuiet/every) {
		bw.step(b, false)
	}
	if bw.reassocs != 0 {
		t.Fatalf("reassociated with no IPv4 counters: %d", bw.reassocs)
	}
}

// A link that went down and came back (quiet hours, a router restarting) is not reassociated at the
// first look for the hours of quiet before it: the network has to be heard steadily again first.
func TestALinkBackFromDownStartsOver(t *testing.T) {
	bw := newBcWorld()
	b := bw.watch()
	for range 20 {
		bw.step(b, true)
	}
	b.forget() // what moved() does when it finds the link down
	bw.now = bw.now.Add(6 * time.Hour)
	for range int(bcQuiet/every) + 2 {
		bw.step(b, false)
	}
	if bw.reassocs != 0 {
		t.Fatalf("reassociated %d times on a link just back, with nothing heard since", bw.reassocs)
	}
	var none *broadcast
	none.forget() // a device that does not watch broadcasts has none to forget
}
