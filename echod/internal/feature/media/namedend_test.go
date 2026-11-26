package media

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// ends records what namedEnd was told, by title.
type ends struct {
	mu  sync.Mutex
	got map[*Title][]End
}

func (e *ends) told(t *Title, how End) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.got == nil {
		e.got = map[*Title][]End{}
	}
	e.got[t] = append(e.got[t], how)
}

func (e *ends) of(t *Title) []End {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]End(nil), e.got[t]...)
}

func (e *ends) wait(t *testing.T, title *Title, want End) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if got := e.of(title); len(got) > 0 {
			if len(got) != 1 || got[0] != want {
				t.Errorf("told %v, want once %v", got, want)
			}
			return
		}
	}
	t.Fatalf("never told of the end; want %v", want)
}

// A track started with a title has its end told once, as what it was: replaced by the next track,
// stopped, played to its end, or failed. Named follows only that track, and only while it is loaded.
func TestANamedTracksEndIsToldOnce(t *testing.T) {
	s := liveStream(t)
	var e ends
	s.namedEnd = e.told
	url := station(t, nil)

	first, second := &Title{Title: "first"}, &Title{Title: "second"}
	s.PlayTitled(url, first)
	if _, _, _, ok := s.Named(first); !ok {
		t.Error("Named does not see the track loaded")
	}
	if _, _, _, ok := s.Named(&Title{Title: "first"}); ok {
		t.Error("Named took another title with the same words for the track")
	}
	s.PlayTitled(url, second)
	e.wait(t, first, EndReplaced)
	if _, _, _, ok := s.Named(first); ok {
		t.Error("Named still sees a replaced track")
	}
	s.Pause()
	if paused, _, _, ok := s.Named(second); !ok || !paused {
		t.Errorf("paused %v, loaded %v", paused, ok)
	}
	s.Stop()
	e.wait(t, second, EndStopped)

	// A short one that plays out.
	short := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wave(fmtChunk(speaker.Channels, speaker.Rate, 16), make([]byte, 4*480)))
	}))
	defer short.Close()
	third := &Title{Title: "third"}
	s.PlayTitled(short.URL, third)
	e.wait(t, third, EndItself)

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer down.Close()
	fourth := &Title{Title: "fourth"}
	s.PlayTitled(down.URL, fourth)
	e.wait(t, fourth, EndFailed)

	// Something else taking the speaker (a remote) is a replacement too.
	fifth := &Title{Title: "fifth"}
	s.PlayTitled(url, fifth)
	s.stopLoaded()
	e.wait(t, fifth, EndReplaced)

	// A track without a title tells nobody.
	s.Play(url)
	s.Stop()
	time.Sleep(20 * time.Millisecond)
	e.mu.Lock()
	n := len(e.got)
	e.mu.Unlock()
	if n != 5 {
		t.Errorf("told of %d tracks, want 5", n)
	}
}
