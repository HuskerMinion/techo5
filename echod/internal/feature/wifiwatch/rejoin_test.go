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
	)
	if err := f(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"reassociate", "joined?", "joined?", "joined?", "refresh", "say"}
	if !slices.Equal(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestRefreshSkippedWhenReassociateFailsOrLinkStaysDown(t *testing.T) {
	refreshed := 0
	refresh := func(context.Context) error { refreshed++; return nil }
	nosay := func(string, ...any) {}
	f := refreshing(func(context.Context) error { return errors.New("no") },
		func(context.Context) bool { return true }, refresh, nosay, func(time.Duration) {})
	if f(context.Background()) == nil || refreshed != 0 {
		t.Fatal("a failed reassociation must be returned and not refreshed")
	}
	f = refreshing(func(context.Context) error { return nil },
		func(context.Context) bool { return false }, refresh, nosay, func(time.Duration) {})
	if err := f(context.Background()); err != nil || refreshed != 0 {
		t.Fatalf("link never back: err=%v refreshed=%d", err, refreshed)
	}
}
