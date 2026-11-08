package home

import (
	"image"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// coverServer serves a test picture over https, failing the first n requests for fail, and holding
// each request for hold first.
func coverServer(t *testing.T, fail int32, hold time.Duration) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	pic := testJPEG(t, 300, 300)
	var asked atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := asked.Add(1)
		select {
		case <-time.After(hold):
		case <-r.Context().Done():
			return
		}
		if r.URL.Path == "/fail" && n <= fail {
			http.Error(w, "no", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(pic)
	}))
	was := artClient
	artClient = srv.Client()
	t.Cleanup(func() {
		artClient = was
		ReceivedArt("", "")
		srv.Close()
	})
	return srv, &asked
}

// waitFor polls until ok or a second has passed.
func waitFor(ok func() bool) bool {
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return true
		}
	}
	return ok()
}

// A song's cover is fetched and shown for that receiver only; a new song drops the old picture at once.
func TestReceivedArtShowsTheSongsCover(t *testing.T) {
	if !hasScreen {
		t.Skip("no screen")
	}
	srv, _ := coverServer(t, 0, 0)
	ReceivedArt(SpotifyName, srv.URL+"/a")
	if !waitFor(func() bool { a, _ := receivedArt(SpotifyName); return a != nil }) {
		t.Fatal("the cover never came")
	}
	if _, thumb := receivedArt(SpotifyName); thumb == nil || thumb.Bounds().Dx() != 640 {
		t.Fatal("Spotify cover was reduced to a small thumbnail")
	}
	if a, _ := receivedArt(AirPlayName); a != nil {
		t.Error("Spotify's cover shown for AirPlay")
	}
	ReceivedArt(SpotifyName, "http://example.invalid/b") // not https: no picture
	if a, _ := receivedArt(SpotifyName); a != nil {
		t.Error("the last song's cover stayed")
	}
}

// A cover sent as the picture itself (AirPlay's) is shown for that receiver, the same picture again is
// left as it is, and none clears it.
func TestReceivedPictureShowsTheCover(t *testing.T) {
	if !hasScreen {
		t.Skip("no screen")
	}
	t.Cleanup(func() { ReceivedPicture("", nil) })
	pic := testJPEG(t, 300, 300)
	ReceivedPicture(AirPlayName, pic)
	if !waitFor(func() bool { a, _ := receivedArt(AirPlayName); return a != nil }) {
		t.Fatal("the cover never came")
	}
	first, _ := receivedArt(AirPlayName)
	ReceivedPicture(AirPlayName, append([]byte(nil), pic...)) // the next song on the album
	if a, _ := receivedArt(AirPlayName); a != first {
		t.Error("the same picture was laid out again")
	}
	ReceivedPicture(AirPlayName, nil)
	if a, _ := receivedArt(AirPlayName); a != nil {
		t.Error("no picture left a cover")
	}
}

// A picture that cannot be read shows nothing, even once its decode has had time to finish, and is not
// decoded again when it is sent again.
func TestReceivedPictureThatCannotBeRead(t *testing.T) {
	if !hasScreen {
		t.Skip("no screen")
	}
	var decodes atomic.Int32
	was := layoutPicture
	layoutPicture = func(b []byte, logo bool, what string) (*image.RGBA, *image.RGBA, error) {
		decodes.Add(1)
		return was(b, logo, what)
	}
	t.Cleanup(func() {
		layoutPicture = was
		ReceivedPicture("", nil)
		received.mu.Lock()
		received.bad = ""
		received.mu.Unlock()
	})
	ReceivedPicture(AirPlayName, testJPEG(t, 300, 300))
	if !waitFor(func() bool { a, _ := receivedArt(AirPlayName); return a != nil }) {
		t.Fatal("the cover never came")
	}
	junk := []byte("not a picture")
	ReceivedPicture(AirPlayName, junk)
	if !waitFor(func() bool {
		received.mu.Lock()
		defer received.mu.Unlock()
		return received.cancel == nil
	}) {
		t.Fatal("the decode never finished")
	}
	if a, _ := receivedArt(AirPlayName); a != nil {
		t.Error("a picture that cannot be read left a cover")
	}
	received.mu.Lock()
	bad := received.bad
	received.mu.Unlock()
	if bad == "" {
		t.Error("the picture that cannot be read was not remembered")
	}
	ReceivedPicture(AirPlayName, junk)
	if a, _ := receivedArt(AirPlayName); a != nil {
		t.Error("sent again, it left a cover")
	}
	if n := decodes.Load(); n != 2 {
		t.Errorf("decoded %d times, want 2: the cover, and the unreadable picture once", n)
	}
}

// One receiver saying it has no cover, as its session ends, leaves the other receiver's alone.
func TestOneReceiverDoesNotClearTheOthersCover(t *testing.T) {
	if !hasScreen {
		t.Skip("no screen")
	}
	srv, _ := coverServer(t, 0, 0)
	ReceivedArt(SpotifyName, srv.URL+"/a")
	if !waitFor(func() bool { a, _ := receivedArt(SpotifyName); return a != nil }) {
		t.Fatal("Spotify's cover never came")
	}
	ReceivedPicture(AirPlayName, nil)
	ReceivedArt(AirPlayName, "")
	if a, _ := receivedArt(SpotifyName); a == nil {
		t.Error("AirPlay ending took Spotify's cover")
	}
	ReceivedPicture(AirPlayName, testJPEG(t, 300, 300))
	if !waitFor(func() bool { a, _ := receivedArt(AirPlayName); return a != nil }) {
		t.Fatal("AirPlay's cover never came")
	}
	ReceivedArt(SpotifyName, "")
	if a, _ := receivedArt(AirPlayName); a == nil {
		t.Error("Spotify stopping took AirPlay's cover")
	}
	ReceivedPicture(AirPlayName, nil)
	if a, _ := receivedArt(AirPlayName); a != nil {
		t.Error("AirPlay ending left its own cover")
	}
}

// Skipping on calls off the fetch for the song left behind, and only the newest song's cover is shown,
// even when the same song comes round again while its first fetch is still out.
func TestReceivedArtKeepsOnlyTheNewest(t *testing.T) {
	if !hasScreen {
		t.Skip("no screen")
	}
	srv, _ := coverServer(t, 0, 200*time.Millisecond)
	ReceivedArt(SpotifyName, srv.URL+"/a")
	ReceivedArt(SpotifyName, srv.URL+"/b")
	ReceivedArt(SpotifyName, srv.URL+"/a") // back to the first song
	if !waitFor(func() bool { a, _ := receivedArt(SpotifyName); return a != nil }) {
		t.Fatal("the newest cover never came")
	}
	received.mu.Lock()
	url, cancel := received.key, received.cancel
	received.mu.Unlock()
	if url != srv.URL+"/a" || cancel != nil {
		t.Errorf("shown for %q with a fetch still out: %v", url, cancel != nil)
	}
}

// A fetch that failed is tried again when the next song names the same cover, as the next track on an
// album does; one that worked is not fetched twice.
func TestReceivedArtRetriesAFailedCover(t *testing.T) {
	if !hasScreen {
		t.Skip("no screen")
	}
	srv, asked := coverServer(t, 1, 0)
	ReceivedArt(SpotifyName, srv.URL+"/fail")
	if !waitFor(func() bool {
		received.mu.Lock()
		defer received.mu.Unlock()
		return received.cancel == nil
	}) {
		t.Fatal("the first fetch never finished")
	}
	if a, _ := receivedArt(SpotifyName); a != nil {
		t.Fatal("a failed fetch showed a picture")
	}
	ReceivedArt(SpotifyName, srv.URL+"/fail") // the next track on the album
	if !waitFor(func() bool { a, _ := receivedArt(SpotifyName); return a != nil }) {
		t.Fatal("the cover was not tried again")
	}
	ReceivedArt(SpotifyName, srv.URL+"/fail") // and the one after: already shown
	time.Sleep(50 * time.Millisecond)
	if n := asked.Load(); n != 2 {
		t.Errorf("asked %d times, want 2", n)
	}
}
