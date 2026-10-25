package home

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// useHomeAssistant points the hass client at srv.
func useHomeAssistant(t *testing.T, srv *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	config.Use(filepath.Join(dir, "state.json"))
	was := hass.Path
	hass.Path = filepath.Join(dir, "hass.json")
	t.Cleanup(func() { hass.Path = was })
	if err := hass.Get().Set(srv.URL, "token"); err != nil {
		t.Fatal(err)
	}
}

// slowHA is Home Assistant serving a picture only when told to: door.png as a still and
// camera.front_door as a snapshot. A request waits for release (closed to let every one through),
// and the server counts the requests it has had and the most it has had in flight at once. A request
// the device gave up on is in flight here until the server notices the connection closed, a moment
// after the device let go of it, so served keeps the span of each request that was served, the ones a
// test of "one at a time" goes by.
type slowHA struct {
	mu      sync.Mutex
	served  []servedSpan
	release chan struct{}
	open    *sync.Once // lets release be closed by the test and by the cleanup
	started atomic.Int32
	hits    atomic.Int32
	live    atomic.Int32
	most    atomic.Int32
}

type servedSpan struct{ start, end time.Time }

// mostServed is the most requests that were served (not given up on) in flight at once.
func (s *slowHA) mostServed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	most := 0
	for _, a := range s.served {
		n := 0
		for _, b := range s.served {
			if !b.start.After(a.start) && b.end.After(a.start) {
				n++
			}
		}
		most = max(most, n)
	}
	return most
}

func newSlowHA(t *testing.T) *slowHA {
	t.Helper()
	var still, snap bytes.Buffer
	if err := png.Encode(&still, image.NewRGBA(image.Rect(0, 0, 1920, 1080))); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&snap, image.NewRGBA(image.Rect(0, 0, 640, 360)), nil); err != nil {
		t.Fatal(err)
	}
	s := &slowHA{release: make(chan struct{})}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		switch r.URL.Path {
		case "/local/door.png":
			body = still.Bytes()
		case "/api/camera_proxy/camera.front_door":
			body = snap.Bytes()
		case "/api/states": // the camera list, which a live view asks for
			_, _ = w.Write([]byte("[]"))
			return
		default:
			http.NotFound(w, r)
			return
		}
		s.started.Add(1)
		start := time.Now()
		n := s.live.Add(1)
		defer s.live.Add(-1)
		for {
			m := s.most.Load()
			if n <= m || s.most.CompareAndSwap(m, n) {
				break
			}
		}
		select {
		case <-s.release:
		case <-r.Context().Done():
			return
		}
		s.hits.Add(1)
		// The span ends before the bytes go: once they have, the client may start the next fetch before
		// this handler gets to take the time, and two fetches one after the other would look like two
		// at once (a busy CI runner did).
		s.mu.Lock()
		s.served = append(s.served, servedSpan{start, time.Now()})
		s.mu.Unlock()
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	// Cleanups run last in, first out: a request still waiting is let go before the server is closed,
	// which would otherwise wait for it.
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(s.release) }) })
	s.open = &once
	useHomeAssistant(t, srv)
	return s
}

// A still taken down while its picture is on its way stays down: the picture arriving does not put
// the view back up.
func TestATappedStillStaysDownWhenItsPictureArrives(t *testing.T) {
	ha := newSlowHA(t)
	f := newFeature(t)
	if err := f.ShowPicture("/local/door.png", Caption{ID: "1", Message: "x"}, time.Minute, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the fetch to start", func() bool { return ha.started.Load() == 1 })
	f.HideCamera()
	ha.open.Do(func() { close(ha.release) })
	eventually(t, "the fetch to finish", func() bool { return ha.hits.Load() == 1 })
	time.Sleep(200 * time.Millisecond) // long enough for the fetch to land, were it going to
	if v, up := f.Camera(); up {
		t.Errorf("view %+v is back up after its picture arrived", v)
	}
	if f.PictureUp("1") {
		t.Error("PictureUp says the notification's view is up")
	}
}

// The same for a live camera, tapped away before its first frame: the frame arriving does not
// start the view again.
func TestATappedCameraStaysDownWhenItsFirstFrameArrives(t *testing.T) {
	ha := newSlowHA(t)
	f := newFeature(t)
	if err := f.ShowPicture("camera.front_door", Caption{ID: "1", Message: "x"}, time.Minute, "off"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the request to start", func() bool { return ha.started.Load() >= 1 })
	f.HideCamera()
	ha.open.Do(func() { close(ha.release) })
	eventually(t, "the frame to be served", func() bool { return ha.hits.Load() >= 1 })
	time.Sleep(200 * time.Millisecond)
	if v, up := f.Camera(); up {
		t.Errorf("view %+v is back up after its first frame arrived", v)
	}
	if f.PictureUp("1") {
		t.Error("PictureUp says the notification's view is up")
	}
}

// A plain camera tapped before its first frame stays down as well.
func TestAPlainCameraTappedBeforeItsFirstFrameStaysDown(t *testing.T) {
	ha := newSlowHA(t)
	f := newFeature(t)
	f.showCamera("camera.front_door", time.Minute, false)
	eventually(t, "the request to start", func() bool { return ha.started.Load() >= 1 })
	f.HideCamera()
	ha.open.Do(func() { close(ha.release) })
	eventually(t, "the frame to be served", func() bool { return ha.hits.Load() >= 1 })
	time.Sleep(200 * time.Millisecond)
	if v, up := f.Camera(); up {
		t.Errorf("view %+v is back up after its first frame arrived", v)
	}
}

// A burst of stills is fetched one at a time, and the ones that were replaced before their turn are
// dropped without a fetch or a decode.
func TestABurstOfStillsIsFetchedOneAtATime(t *testing.T) {
	ha := newSlowHA(t)
	f := newFeature(t)
	decoded := stillsDecoded.Load()
	for i := 1; i <= 5; i++ {
		if i == 2 {
			// The first is on its way when the rest replace it, so it is the one whose bytes arrive
			// for a view that is gone.
			eventually(t, "the first fetch to start", func() bool { return ha.started.Load() == 1 })
		}
		if err := f.ShowPicture("/local/door.png", Caption{ID: strconv.Itoa(i), Message: "x"}, time.Minute, ""); err != nil {
			t.Fatal(err)
		}
	}
	ha.open.Do(func() { close(ha.release) })
	eventually(t, "the last picture", func() bool { v, _ := f.Camera(); return v.Frame != nil && v.Caption.ID == "5" })
	time.Sleep(200 * time.Millisecond) // long enough for a stale fetch to start, were one going to
	if m := ha.mostServed(); m != 1 {
		t.Errorf("%d fetches in flight at once, want 1", m)
	}
	if n := ha.started.Load(); n > 2 {
		t.Errorf("%d pictures fetched, want at most 2: the one already on its way and the last", n)
	}
	if n := stillsDecoded.Load() - decoded; n != 1 {
		t.Errorf("%d pictures decoded, want only the last one's", n)
	}
	if v, _ := f.Camera(); v.Caption.ID != "5" {
		t.Errorf("view is notification %s's, want 5's", v.Caption.ID)
	}
}

// HidePicture takes down only the view of the notification it is told about.
func TestHidePictureHidesOnlyItsOwnNotification(t *testing.T) {
	newSlowHA(t)
	f := newFeature(t)
	if err := f.ShowPicture("/local/door.png", Caption{ID: "1", Message: "x"}, time.Minute, ""); err != nil {
		t.Fatal(err)
	}
	if f.HidePicture("2") {
		t.Error("HidePicture(2) said it hid notification 1's view")
	}
	if !f.PictureUp("1") {
		t.Error("HidePicture(2) took down notification 1's view")
	}
	if !f.HidePicture("1") {
		t.Error("HidePicture(1) said it hid nothing")
	}
	if _, up := f.Camera(); up || f.PictureUp("1") {
		t.Error("notification 1's view is still up")
	}
	if f.HidePicture("1") {
		t.Error("HidePicture(1) hid a view that was already down")
	}
}

// Taking the camera down forgets whose words were on it.
func TestHideCameraClearsTheCaption(t *testing.T) {
	newSlowHA(t)
	f := newFeature(t)
	if err := f.ShowPicture("/local/door.png", Caption{ID: "1", Message: "x"}, time.Minute, ""); err != nil {
		t.Fatal(err)
	}
	f.HideCamera()
	f.mu.Lock()
	c := f.cam.Caption
	f.mu.Unlock()
	if c != nil {
		t.Errorf("caption %+v left on a view taken down", c)
	}
}

func TestPictureIsLive(t *testing.T) {
	for src, want := range map[string]bool{
		"camera.front_door":        true,
		"image.doorbell":           false,
		"/local/door.png":          false,
		"https://example.com/a":    false,
		"":                         false,
		"sensor.camera.not_really": false,
	} {
		if got := PictureIsLive(src); got != want {
			t.Errorf("PictureIsLive(%q) = %v, want %v", src, got, want)
		}
	}
}

// A picture address that is not Home Assistant's own is held to the video player's rules: not the
// device itself, not a link-local address.
func TestCheckPictureRefusesTheDevicesOwnAddresses(t *testing.T) {
	for _, src := range []string{"HTTP://127.0.0.1/x.jpg", "http://127.0.0.1/x.jpg", "http://localhost/x.jpg", "http://169.254.1.1/x.jpg", "http://[::1]/x.jpg"} {
		err := CheckPicture(src)
		if err == nil {
			t.Errorf("%s: accepted", src)
		} else if !strings.Contains(err.Error(), "picture") {
			t.Errorf("%s: refusal %q does not say what it refused", src, err)
		}
	}
	if err := CheckPicture("HTTPS://example.com/x.jpg"); err != nil {
		t.Errorf("an upper-case scheme was refused: %v", err)
	}
	for _, src := range []string{"ftp://user:pass@nas/x.jpg", "HTTP://user:pass@127.0.0.1/x.jpg", "nas://user:pass@nas/x.jpg"} {
		if err := CheckPicture(src); err == nil || strings.Contains(err.Error(), "pass") || strings.Contains(err.Error(), "user") {
			t.Errorf("%s: refusal %v must exist and must not repeat the login", src, err)
		}
	}
	if err := CheckPicture("https://example.com/x.jpg"); err != nil {
		t.Errorf("https://example.com/x.jpg: refused: %v", err)
	}
	if err := CheckPicture("http://admin:hunter2@127.0.0.1/x.jpg"); err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("refusal %v must exist and must not repeat the login", err)
	}
}

// Home Assistant's own address is fetched as a path with the token, so it is not held to the host
// rules even though a test (or a home server on the same machine) has it on loopback.
func TestCheckPictureTakesHomeAssistantsOwnAddress(t *testing.T) {
	_, base := fakePicturesAt(t)
	if err := CheckPicture(base + "/local/door.png"); err != nil {
		t.Errorf("Home Assistant's own address refused: %v", err)
	}
}

// What hass.FetchURL says about a reply that is not a success carries no login: whatever the error
// says is drawn on the screen.
func TestAFailedFetchShowsNoLogin(t *testing.T) {
	for _, in := range []string{
		"hass: GET http://user:secret@cam.example/x.jpg: 401 Unauthorized",
		"hass: GET http://user@cam.example/x.jpg?k=1: 401 Unauthorized",
		`Get "http://user:***@cam.example/x.jpg": dial tcp: no such host`,
	} {
		got := redactError(in)
		if strings.Contains(got, "user") || strings.Contains(got, "secret") || strings.Contains(got, "***") {
			t.Errorf("redactError(%q) = %q, still has the login", in, got)
		}
		if !strings.Contains(got, "cam.example/x.jpg") {
			t.Errorf("redactError(%q) = %q, lost the address", in, got)
		}
	}
}

func init() {
	// A view's frame that fails is asked for again after this long; a test that takes the view down
	// should not wait two seconds for the loop to notice.
	cameraRetry = 10 * time.Millisecond
}

// newFeature is a Feature for a test that may put a camera or picture up. When the test ends the view
// is taken down and the test waits for every goroutine the view started, so that none of them is still
// reading the config or fetching from Home Assistant when the next test swaps those out - or when the
// next run of the same test does, under -count.
func newFeature(t *testing.T) *Feature {
	t.Helper()
	f := &Feature{}
	t.Cleanup(func() {
		f.HideCamera()
		f.loops.Wait()
	})
	return f
}

// Every view has its own number, and HideView takes down only the view it was read from: a tap read
// before a newer view replaced the old one must not hide the newer.
func TestHideViewHidesOnlyThatView(t *testing.T) {
	newSlowHA(t)
	f := newFeature(t)
	f.ShowCamera("camera.front_door", time.Minute)
	first, _ := f.Camera()
	f.ShowCamera("camera.deck", time.Minute)
	second, up := f.Camera()
	if !up || second.View == 0 || second.View == first.View {
		t.Fatalf("views %d then %d (up %v), want two different numbers", first.View, second.View, up)
	}
	if f.HideView(first.View) {
		t.Error("HideView of a replaced view said it hid something")
	}
	if v, up := f.Camera(); !up || v.View != second.View {
		t.Fatalf("view %+v up=%v after hiding the replaced one, want the newer still up", v, up)
	}
	if !f.HideView(second.View) {
		t.Error("HideView of the current view said it hid nothing")
	}
	if _, up := f.Camera(); up {
		t.Error("the current view is still up after HideView")
	}
	if f.HideView(second.View) {
		t.Error("HideView of a view already down said it hid something")
	}
}

// The same camera asked for again is the same thing on screen and keeps its number, so a tap read
// before the re-show still closes it. A notification's picture over a plain view of the same camera is
// not: it gets a number of its own, or that tap would hide the notification's picture.
func TestAReshownCameraKeepsItsViewNumber(t *testing.T) {
	newSlowHA(t)
	f := newFeature(t)
	f.ShowCamera("camera.front_door", time.Minute)
	first, _ := f.Camera()
	f.ShowCamera("camera.front_door", 2*time.Minute)
	again, _ := f.Camera()
	if again.View != first.View {
		t.Errorf("view %d after a re-show, want %d kept", again.View, first.View)
	}
	if err := f.ShowPicture("camera.front_door", Caption{ID: "1", Message: "x"}, time.Minute, "off"); err != nil {
		t.Fatal(err)
	}
	pic, _ := f.Camera()
	if pic.View == first.View {
		t.Error("a notification's picture over the plain view kept its number")
	}
	if f.HideView(first.View) {
		t.Error("a tap on the plain view hid the notification's picture")
	}
}
