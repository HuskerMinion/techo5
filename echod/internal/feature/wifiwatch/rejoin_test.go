package wifiwatch

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

// The filter is refreshed only after the link is back up, never while it is still down.
func TestRefreshWaitsForTheLinkThenRefreshes(t *testing.T) {
	var events []string
	polls := 0
	f := refreshing(
		func(context.Context) error { events = append(events, "reassociate"); return nil },
		func(context.Context) bool { polls++; events = append(events, "joined?"); return polls >= 3 },
		func(context.Context) error { events = append(events, "refresh"); return nil },
		func(string, ...any) { events = append(events, "say") },
		func(time.Duration) {},
		func(f func()) { f() },
	)
	if err := f(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"reassociate", "joined?", "joined?", "joined?", "refresh", "say", "joined?", "refresh"}
	if !slices.Equal(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestRefreshSkippedWhenReassociateFailsOrLinkStaysDown(t *testing.T) {
	refreshed := 0
	refresh := func(context.Context) error { refreshed++; return nil }
	nosay := func(string, ...any) {}
	f := refreshing(func(context.Context) error { return errors.New("no") },
		func(context.Context) bool { return true }, refresh, nosay, func(time.Duration) {}, func(f func()) { f() })
	if f(context.Background()) == nil || refreshed != 0 {
		t.Fatal("a failed reassociation must be returned and not refreshed")
	}
	f = refreshing(func(context.Context) error { return nil },
		func(context.Context) bool { return false }, refresh, nosay, func(time.Duration) {}, func(f func()) { f() })
	if err := f(context.Background()); err != nil || refreshed != 0 {
		t.Fatalf("link never back: err=%v refreshed=%d", err, refreshed)
	}
}

// The watcher that asked is not held for the wait: the wait and the refresh run on their own, and a
// second reassociation while one is waiting starts no second one.
func TestTheWaitIsNotTheWatchers(t *testing.T) {
	var queued []func()
	f := refreshing(func(context.Context) error { return nil },
		func(context.Context) bool { return true },
		func(context.Context) error { return nil },
		func(string, ...any) {}, func(time.Duration) {},
		func(g func()) { queued = append(queued, g) })
	_ = f(context.Background())
	_ = f(context.Background())
	if len(queued) != 1 {
		t.Fatalf("%d waits started for two reassociations while the first was waiting, want 1", len(queued))
	}
	queued[0]()
	_ = f(context.Background())
	if len(queued) != 2 {
		t.Fatal("once the first wait was done, the next reassociation started none")
	}
}
