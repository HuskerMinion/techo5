package media

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An add-on's audio is never fetched from the device itself: loopback is refused as it is dialed,
// where the stations' own client reaches it.
func TestAnAddonsAudioIsNotFetchedFromTheDevice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("x")) }))
	defer srv.Close()
	if resp, err := guardedClient.Get(srv.URL); err == nil {
		resp.Body.Close()
		t.Fatal("the guarded client fetched from loopback")
	} else if !strings.Contains(err.Error(), "may not reach") {
		t.Errorf("refused for another reason: %v", err)
	}
	resp, err := streamClient.Get(srv.URL)
	if err != nil {
		t.Fatalf("the stations' client: %v", err)
	}
	resp.Body.Close()
}

// PlayNamed, the add-ons' way to play, plays guarded.
func TestPlayGuardedMarksTheTrack(t *testing.T) {
	s := liveStream(t)
	tr := s.play("http://192.0.2.1/a.mp3", false, nil, true)
	if tr == nil || !tr.guarded {
		t.Fatal("the track is not guarded")
	}
	s.Stop()
}
