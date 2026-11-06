package wifiwatch

import (
	"context"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// mcWorld is what the multicast watcher sees, set by the test.
type mcWorld struct {
	now       time.Time
	g         wifi.Group
	up        bool
	refreshes int
	said      []string
}

func (mw *mcWorld) watch() *multicast {
	return &multicast{
		now:          func() time.Time { return mw.now },
		groupPackets: func() (wifi.Group, bool) { return mw.g, true },
		joined:       func(context.Context) bool { return mw.up },
		gatewayUp:    func(context.Context) bool { return mw.up },
		refresh:      func(context.Context) error { mw.refreshes++; return nil },
		say:          func(msg string, _ ...any) { mw.said = append(mw.said, msg) },
		wait:         mcFirstWait,
	}
}

// step moves the clock on by one look; heard says whether the network sent multicast in it. The
// device sends some of its own every look, which comes back to it and must not count.
func (mw *mcWorld) step(m *multicast, heard bool) {
	mw.now = mw.now.Add(every)
	mw.g.McastOut += 3
	mw.g.McastIn += 3
	if heard {
		mw.g.McastIn += 2
	}
	m.look(context.Background())
}

// Multicast that stops for mcQuiet while the link is up gets the filter refreshed once, then not again
// before the wait; when it comes back the wait starts over.
func TestMulticastThatStopsIsRefreshed(t *testing.T) {
	mw := &mcWorld{now: time.Date(2026, 11, 6, 3, 0, 0, 0, time.UTC), up: true, g: wifi.Group{From: wifi.FromIPv4}}
	m := mw.watch()
	for range 4 {
		mw.step(m, true)
	}
	for i := 0; i < int(mcQuiet/every)-1; i++ {
		mw.step(m, false)
	}
	if mw.refreshes != 0 {
		t.Fatalf("refreshed after %d looks of quiet, before mcQuiet", int(mcQuiet/every)-1)
	}
	mw.step(m, false)
	if mw.refreshes != 1 {
		t.Fatalf("refreshes = %d after mcQuiet of quiet, want 1", mw.refreshes)
	}
	for range int(mcFirstWait/every) - 1 {
		mw.step(m, false)
	}
	if mw.refreshes != 1 {
		t.Fatalf("refreshes = %d within the wait, want 1", mw.refreshes)
	}
	mw.step(m, false)
	if mw.refreshes != 2 || m.wait != 4*mcFirstWait {
		t.Fatalf("refreshes = %d, wait %v after the first wait; want 2 and %v", mw.refreshes, m.wait, 4*mcFirstWait)
	}
	mw.step(m, true)
	if m.wait != mcFirstWait || !m.tried.IsZero() {
		t.Fatalf("wait %v after multicast came back, want %v", m.wait, mcFirstWait)
	}
}

// A network never heard sending multicast this boot, and a link that is down, are left alone.
func TestMulticastNeverHeardOrLinkDownIsLeftAlone(t *testing.T) {
	mw := &mcWorld{now: time.Date(2026, 11, 6, 3, 0, 0, 0, time.UTC), up: true, g: wifi.Group{From: wifi.FromIPv4}}
	m := mw.watch()
	for range 3 * int(mcQuiet/every) {
		mw.step(m, false)
	}
	if mw.refreshes != 0 {
		t.Fatalf("refreshed a network never heard: %d", mw.refreshes)
	}
	mw.step(m, true)
	mw.up = false
	for range 3 * int(mcQuiet/every) {
		mw.step(m, false)
	}
	if mw.refreshes != 0 {
		t.Fatalf("refreshed a link that is down: %d", mw.refreshes)
	}
}
