package media

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// endingStream serves a converted stream that ends cleanly at once, and counts how often it was fetched.
func endingStream(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(testWAV(0))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &hits
}

// liveOnly makes the radio feature say only the urls in stations are live, and counts what it is asked.
func liveOnly(t *testing.T, stations ...string) *atomic.Int32 {
	t.Helper()
	shortWaits(t)
	old := liveCheck.Load()
	t.Cleanup(func() { liveCheck.Store(old) })
	var asked atomic.Int32
	SetLive(func(url string) bool {
		asked.Add(1)
		for _, s := range stations {
			if s == url {
				return true
			}
		}
		return false
	})
	return &asked
}

func liveStream(t *testing.T) *Stream {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	return NewStream(speaker.NewDriver(speaker.New()), speaker.New(), func() {}, func(string) {})
}

func waitDone(t *testing.T, tr *track) {
	t.Helper()
	select {
	case <-tr.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the track did not end")
	}
}

// A url the radio feature did not start for a station (a song, a podcast, a file of Home Assistant's)
// that ends cleanly is played once and ends, however the radio page names what plays.
func TestATrackThatIsNotAStationIsNotFetchedAgainWhenItEnds(t *testing.T) {
	url, hits := endingStream(t)
	liveOnly(t, "http://station.invalid/live")
	s := liveStream(t)
	tr := s.play(url, false, nil)
	waitDone(t, tr)
	if n := hits.Load(); n != 1 {
		t.Errorf("a track that ended cleanly was fetched %d times, want once", n)
	}
	if tr.live.Load() {
		t.Error("a track that is not a station was taken for live radio")
	}
}

// A short clip played after the radio is not live because the radio was: each track is asked about as it
// starts, and the clip is not the station.
func TestAClipAfterTheRadioIsPlayedOnce(t *testing.T) {
	station := overStream(t)
	clip, hits := endingStream(t)
	liveOnly(t, station)
	s := liveStream(t)
	radio := s.play(station, false, nil)
	if !radio.live.Load() {
		t.Fatal("the station was not taken for live radio")
	}
	tr := s.play(clip, false, nil)
	waitDone(t, tr)
	if n := hits.Load(); n != 1 {
		t.Errorf("a clip after the radio was fetched %d times, want once", n)
	}
}

// Switching stations goes by the new track: one the radio feature does not call live is not, though the
// one before it was, and the radio feature is asked once per track rather than on every drop.
func TestASwitchGoesByTheNewTrack(t *testing.T) {
	a := overStream(t)
	b, _ := endingStream(t)
	asked := liveOnly(t, a)
	s := liveStream(t)
	first := s.play(a, false, nil)
	second := s.play(b, false, nil)
	waitDone(t, second)
	if !first.live.Load() || second.live.Load() {
		t.Errorf("live: first %t, second %t; want true, false", first.live.Load(), second.live.Load())
	}
	if n := asked.Load(); n != 2 {
		t.Errorf("the radio feature was asked %d times for two tracks", n)
	}
}

// A real station whose stream ends is fetched again, as the same track, until it is plainly gone.
func TestALiveStationThatEndsIsFetchedAgain(t *testing.T) {
	url, hits := endingStream(t)
	asked := liveOnly(t, url)
	var mu sync.Mutex
	ended := 0
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	s := NewStream(speaker.NewDriver(speaker.New()), speaker.New(), func() {}, func(string) {
		mu.Lock()
		ended++
		mu.Unlock()
	})
	tr := s.play(url, false, nil)
	waitDone(t, tr)
	if n := hits.Load(); n != resumeTries+1 {
		t.Errorf("a station that kept ending at once was fetched %d times, want %d", n, resumeTries+1)
	}
	if n := asked.Load(); n != 1 {
		t.Errorf("the radio feature was asked %d times for one track", n)
	}
	waitUntil(t, "the track reported ended", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return ended == 1
	})
}

// A station Home Assistant started, and named only as its stream had started, is live from then on:
// MarkLive takes the track playing now, and only that one.
func TestATrackMarkedLiveAsItStartsIsLive(t *testing.T) {
	station := overStream(t)
	other, _ := endingStream(t)
	liveOnly(t) // the radio feature knows of no station as the track starts
	s := liveStream(t)
	tr := s.play(station, false, nil)
	if tr.live.Load() {
		t.Fatal("the track was live before it was marked")
	}
	if s.markLive(other) || tr.live.Load() {
		t.Fatal("marking another url made the track playing live")
	}
	if !s.markLive(station) || !tr.live.Load() {
		t.Fatal("the track playing was not made live")
	}
	next := s.play(other, false, nil)
	waitDone(t, next)
	if s.markLive(station) {
		t.Fatal("a track no longer playing was marked live")
	}
	if s.markLive(other) {
		t.Fatal("a track that has ended was marked live")
	}
}
