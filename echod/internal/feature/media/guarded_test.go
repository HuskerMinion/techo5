package media

import (
	"errors"
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

// A media address is logged without its login, query or fragment, and an error's words with it.
func TestMediaAddressesAreLoggedSafely(t *testing.T) {
	raw := "https://user:pw@mealie.local/media/a.mp3?token=s3cret#t=3"
	if got := safeURL(raw); strings.Contains(got, "s3cret") || strings.Contains(got, "pw") || got != "https://mealie.local/media/a.mp3?…" {
		t.Errorf("logged as %q", got)
	}
	err := errors.New(`Get "` + raw + `": dial tcp: refused`)
	if got := safeErr(err, raw); strings.Contains(got, "s3cret") {
		t.Errorf("the error logged as %q", got)
	}
	if got := safeURL("rain"); got != "rain" {
		t.Errorf("a sound's name logged as %q", got)
	}
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
