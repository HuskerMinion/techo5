//go:build !dot && !spot

package display

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/musicassistant"
)

// A paused player is where it says; a playing one has moved on since it said so.
func TestLibPositionOfMovesOnWhilePlaying(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	a := map[string]any{"media_position": 100.0, "media_position_updated_at": now.Add(-30 * time.Second).Format(time.RFC3339Nano)}
	if p, ok := libPositionOf(a, "paused", now); !ok || p != 100 {
		t.Errorf("paused at %v (%v); want 100", p, ok)
	}
	if p, ok := libPositionOf(a, "playing", now); !ok || p != 130 {
		t.Errorf("playing at %v (%v); want 130", p, ok)
	}
	if _, ok := libPositionOf(map[string]any{}, "playing", now); ok {
		t.Errorf("a player with no position gave one")
	}
}

func TestLibClock(t *testing.T) {
	for secs, want := range map[int]string{65: "1:05", 3723: "1:02:03", 0: "0:00"} {
		if got := libClock(secs); got != want {
			t.Errorf("libClock(%d) = %q, want %q", secs, got, want)
		}
	}
}

// fakeSpots stands in for the saved places.
type fakeSpots struct {
	mu    sync.Mutex
	spots map[string]int
}

func useFakeSpots(t *testing.T) *fakeSpots {
	f := &fakeSpots{spots: map[string]int{}}
	saveWas, forgetWas, spotWas := libSaveSpot, libForgetSpot, libResumeSpot
	libSaveSpot = func(uri string, s int) { f.mu.Lock(); f.spots[uri] = s; f.mu.Unlock() }
	libForgetSpot = func(uri string) { f.mu.Lock(); delete(f.spots, uri); f.mu.Unlock() }
	libResumeSpot = func(uri string) (int, bool) { f.mu.Lock(); defer f.mu.Unlock(); s, ok := f.spots[uri]; return s, ok }
	t.Cleanup(func() { libSaveSpot, libForgetSpot, libResumeSpot = saveWas, forgetWas, spotWas })
	return f
}

func (f *fakeSpots) get(uri string) (int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.spots[uri]
	return s, ok
}

// A place is kept only once it is worth going back to, and a book played to its end lets its place go.
func TestLibKeepPlace(t *testing.T) {
	f := useFakeSpots(t)
	libKeepPlace("b", 30, 3600)
	if _, ok := f.get("b"); ok {
		t.Errorf("a place half a minute in was kept")
	}
	libKeepPlace("b", 1800, 3600)
	if s, _ := f.get("b"); s != 1800 {
		t.Errorf("place = %d, want 1800", s)
	}
	libKeepPlace("b", 3550, 3600)
	if _, ok := f.get("b"); ok {
		t.Errorf("a book played to its end kept its place")
	}
}

// A different length on the player is something else; a length not known is taken as the book.
func TestLibSameLength(t *testing.T) {
	if !libSameLength(map[string]any{"media_duration": 3602.0}, 3600) {
		t.Errorf("the book's own length was taken for something else")
	}
	if libSameLength(map[string]any{"media_duration": 240.0}, 3600) {
		t.Errorf("a four-minute song was taken for the book")
	}
	if !libSameLength(map[string]any{}, 3600) || !libSameLength(map[string]any{"media_duration": 240.0}, 0) {
		t.Errorf("not knowing a length stopped the watch")
	}
}

// Watching a book: its place is kept when it pauses, and the watch ends, keeping the last place, when
// the player stops.
func TestLibWatchBookKeepsThePlaceWhenPausedAndStopped(t *testing.T) {
	f := useFakeSpots(t)
	firstWas, everyWas, stateWas := libWatchFirst, libWatchEvery, libPlayerState
	t.Cleanup(func() { libWatchFirst, libWatchEvery, libPlayerState = firstWas, everyWas, stateWas })
	libWatchFirst, libWatchEvery = time.Millisecond, time.Millisecond

	var mu sync.Mutex
	steps := []hass.State{
		{State: "playing", Attributes: map[string]any{"media_position": 600.0, "media_duration": 3600.0}},
		{State: "paused", Attributes: map[string]any{"media_position": 900.0, "media_duration": 3600.0}},
		{State: "playing", Attributes: map[string]any{"media_position": 1200.0, "media_duration": 3600.0}},
		{State: "idle"}, {State: "idle"},
	}
	done := make(chan struct{})
	libPlayerState = func(string) (hass.State, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(steps) == 0 {
			select {
			case <-done:
			default:
				close(done)
			}
			return hass.State{}, errors.New("gone")
		}
		s := steps[0]
		steps = steps[1:]
		return s, nil
	}
	libWatchBook("media_player.x", "library://audiobook/1", 3600)
	deadline := time.After(2 * time.Second)
	for {
		if s, ok := f.get("library://audiobook/1"); ok && s == 1200 {
			return
		}
		select {
		case <-deadline:
			s, ok := f.get("library://audiobook/1")
			t.Fatalf("place = %d (%v); want 1200 kept when the player stopped", s, ok)
		case <-time.After(time.Millisecond):
		}
	}
}

// The first looks can still find what was on before the book: the watch waits for the book rather than
// ending before it has kept a place.
func TestLibWatchBookWaitsForTheBookToComeOn(t *testing.T) {
	f := useFakeSpots(t)
	firstWas, everyWas, stateWas := libWatchFirst, libWatchEvery, libPlayerState
	t.Cleanup(func() { libWatchFirst, libWatchEvery, libPlayerState = firstWas, everyWas, stateWas })
	libWatchFirst, libWatchEvery = time.Millisecond, time.Millisecond

	var mu sync.Mutex
	before := hass.State{State: "playing", Attributes: map[string]any{"media_position": 30.0, "media_duration": 200.0}}
	steps := []hass.State{before, before,
		{State: "paused", Attributes: map[string]any{"media_position": 700.0, "media_duration": 3600.0}},
	}
	// The watch's last look is its second with nothing to see: it ends there, so the test waits for it
	// before the cleanup puts the watch's settings back under it.
	ended, gone := make(chan struct{}), 0
	libPlayerState = func(string) (hass.State, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(steps) == 0 {
			if gone++; gone == 2 {
				close(ended)
			}
			return hass.State{}, errors.New("gone")
		}
		s := steps[0]
		steps = steps[1:]
		return s, nil
	}
	libWatchBook("media_player.x", "library://audiobook/2", 3600)
	deadline := time.After(2 * time.Second)
	for {
		if s, ok := f.get("library://audiobook/2"); ok && s == 700 {
			select {
			case <-ended:
			case <-deadline:
				t.Fatal("the watch did not end")
			}
			return
		}
		select {
		case <-deadline:
			s, ok := f.get("library://audiobook/2")
			t.Fatalf("place = %d (%v); want 700, kept once the book came on", s, ok)
		case <-time.After(time.Millisecond):
		}
	}
}

// An audiobook page offers to go on only from a place worth going back to.
func TestLibResumeOffer(t *testing.T) {
	f := useFakeSpots(t)
	p := &libPage{what: libAudiobook, entry: musicassistant.Entry{URI: "library://audiobook/7"}}
	if _, ok := libResumeOffer(p); ok {
		t.Errorf("a book never played offered to resume")
	}
	f.mu.Lock()
	f.spots["library://audiobook/7"] = 4000
	f.mu.Unlock()
	if s, ok := libResumeOffer(p); !ok || s != 4000 {
		t.Errorf("offer = %d (%v); want 4000", s, ok)
	}
	if _, ok := libResumeOffer(&libPage{what: libAlbum, entry: p.entry}); ok {
		t.Errorf("an album page offered to resume")
	}
}
