package home

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// Every kind of picture a notification can name, and the address each is fetched from.
func TestPictureSources(t *testing.T) {
	cases := []struct {
		src     string
		live    bool
		path    string
		refused bool
	}{
		{src: "camera.front_door", live: true, path: "camera.front_door"},
		{src: "image.doorbell_snapshot", path: "/api/image_proxy/image.doorbell_snapshot"},
		{src: "/local/door.jpg", path: "/local/door.jpg"},
		{src: "/api/camera_proxy/camera.deck", path: "/api/camera_proxy/camera.deck"},
		{src: "http://192.168.1.20/snap.jpg", path: "http://192.168.1.20/snap.jpg"},
		{src: "https://example.com/a.png", path: "https://example.com/a.png"},
		{src: "", refused: true},
		{src: "front door", refused: true},
		{src: "ftp://example.com/a.png", refused: true},
		{src: "sensor.washer", refused: true},
	}
	for _, c := range cases {
		live, path, err := pictureSource(c.src)
		if c.refused {
			if err == nil {
				t.Errorf("%q: taken, want refused", c.src)
			}
			if CheckPicture(c.src) == nil {
				t.Errorf("%q: CheckPicture took it", c.src)
			}
			continue
		}
		if err != nil || live != c.live || path != c.path {
			t.Errorf("%q: got live=%v path=%q err=%v, want live=%v path=%q", c.src, live, path, err, c.live, c.path)
		}
	}
}

// fakePictures is Home Assistant serving one picture, door.png, larger than the panel; anything else
// is not found. It counts the fetches of the picture.
func fakePictures(t *testing.T) *atomic.Int32 {
	t.Helper()
	hits, _ := fakePicturesAt(t)
	return hits
}

// fakePicturesAt is fakePictures, and the address Home Assistant is set up at.
func fakePicturesAt(t *testing.T) (*atomic.Int32, string) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1920, 1080))); err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/local/door.png" {
			hits.Add(1)
			_, _ = w.Write(buf.Bytes())
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	config.Use(filepath.Join(dir, "state.json"))
	was := hass.Path
	hass.Path = filepath.Join(dir, "hass.json")
	t.Cleanup(func() { hass.Path = was })
	if err := hass.Get().Set(srv.URL, "token"); err != nil {
		t.Fatal(err)
	}
	return &hits, srv.URL
}

// A still is fetched once, scaled to the panel, and shown with its caption.
func TestAStillIsFetchedOnceAndScaled(t *testing.T) {
	hits := fakePictures(t)
	f := newFeature(t)
	if err := f.ShowPicture("/local/door.png", Caption{ID: "1", Title: "Doorbell", Message: "Someone is here"}, time.Minute, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the picture", func() bool { v, _ := f.Camera(); return v.Frame != nil })
	time.Sleep(200 * time.Millisecond) // long enough for a second fetch, were there one
	if n := hits.Load(); n != 1 {
		t.Errorf("fetched %d times, want once", n)
	}
	v, up := f.Camera()
	if !up || v.Caption == nil || v.Caption.Title != "Doorbell" || v.Caption.Message != "Someone is here" {
		t.Fatalf("view %+v up=%v, want it up with its caption", v, up)
	}
	if b := v.Frame.Bounds(); b.Dx() > cameraFrameW || b.Dy() > cameraFrameH {
		t.Errorf("frame is %v, bigger than the panel's %dx%d", b, cameraFrameW, cameraFrameH)
	}
	if !f.PictureUp("1") || f.PictureUp("2") {
		t.Error("PictureUp should be true for its own notification and only that one")
	}
}

// A run of notifications over the same picture changes only their words: the picture stays on the
// screen from one to the next, and is fetched once. One over another picture is fetched as it comes.
func TestTheSamePictureAgainKeepsItsFrame(t *testing.T) {
	hits := fakePictures(t)
	f := newFeature(t)
	if err := f.ShowPicture("/local/door.png", Caption{ID: "1", Message: "One"}, time.Minute, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the picture", func() bool { v, _ := f.Camera(); return v.Frame != nil })
	first, _ := f.Camera()
	for i, msg := range []string{"Two", "Three"} {
		id := fmt.Sprint(i + 2)
		if err := f.ShowPicture("/local/door.png", Caption{ID: id, Message: msg}, time.Minute, ""); err != nil {
			t.Fatal(err)
		}
		v, up := f.Camera()
		if !up || v.Frame != first.Frame || v.Caption == nil || v.Caption.Message != msg {
			t.Fatalf("view %+v up=%v, want the same frame with %q over it at once", v, up, msg)
		}
		if !f.PictureUp(id) {
			t.Errorf("notification %s is not the one up", id)
		}
	}
	// The picture is still fetched again: it can change behind one address (a snapshot saved over the
	// last), and the newest has to show. A burst fetches only the newest, as each new view cancels the
	// fetch of the one it replaces.
	eventually(t, "the picture fetched again", func() bool { return hits.Load() >= 2 })
	eventually(t, "the newest picture", func() bool { v, _ := f.Camera(); return v.Frame != nil && v.Frame != first.Frame })

	// Another picture's address: a view of its own, fetched.
	if err := f.ShowPicture("/local/door.png?v=2", Caption{ID: "4", Message: "Four"}, time.Minute, ""); err != nil {
		t.Fatal(err)
	}
	if v, _ := f.Camera(); v.Frame != nil {
		t.Error("another picture's view came up with the last picture's frame")
	}
}

// A picture that cannot be fetched still comes up, with its caption and why there is no picture.
func TestAStillThatFailsKeepsItsCaption(t *testing.T) {
	fakePictures(t)
	f := newFeature(t)
	if err := f.ShowPicture("/local/missing.png", Caption{ID: "1", Message: "Washer done"}, time.Minute, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the error", func() bool { v, _ := f.Camera(); return v.Error != "" })
	if v, up := f.Camera(); !up || v.Caption == nil || v.Caption.Message != "Washer done" || v.Frame != nil {
		t.Errorf("view %+v up=%v, want it up with its caption and no frame", v, up)
	}
}

func TestRedactURL(t *testing.T) {
	for in, want := range map[string]string{
		"/local/door.png":                           "/local/door.png",
		"/local/door.png?token=secret":              "/local/door.png",
		"https://x.test/a.jpg?sig=1&b=2#frag":       "https://x.test/a.jpg",
		"https://x.test/a.jpg#frag?not-query":       "https://x.test/a.jpg",
		"camera.front_door":                         "camera.front_door",
		"http://user:pass@cam.example/snap.jpg?x=1": "http://cam.example/snap.jpg",
		"http://user@cam.example:8080/snap.jpg#f":   "http://cam.example:8080/snap.jpg",
		"//user:pass@cam.example/snap.jpg?x=1":      "//cam.example/snap.jpg", // a network-path reference: parsed with its host
		"":                                          "",
	} {
		if got := redactURL(in); got != want {
			t.Errorf("redactURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// A picture that cannot be fetched says why without repeating the address's token, in whatever form
// the address was given: a path, Home Assistant's own full address (which the fetch rewrites to a
// path, query and all, before its error repeats it), or another host's.
func TestAFailedFetchLeaksNoToken(t *testing.T) {
	_, base := fakePicturesAt(t)
	for _, src := range []string{
		"/local/missing.png?token=secret",
		base + "/local/missing.png?token=secret",
		base + "/local/missing.png#secret",
		"http://nothing.invalid/missing.png?token=secret", // no such host: the error is the dial's
	} {
		f := newFeature(t)
		if err := f.ShowPicture(src, Caption{ID: "1", Message: "x"}, time.Minute, ""); err != nil {
			t.Fatal(err)
		}
		// No such host is a DNS answer, and a slow resolver (CI's) took longer than eventually waits.
		within := 3 * time.Second
		if strings.Contains(src, ".invalid") {
			within = 30 * time.Second
		}
		eventuallyWithin(t, within, "the error for "+src, func() bool { v, _ := f.Camera(); return v.Error != "" })
		if v, _ := f.Camera(); strings.Contains(v.Error, "secret") {
			t.Errorf("%s: error %q carries the token", src, v.Error)
		}
	}
}

// Redacting an error takes the query and fragment off every address in it, and keeps the rest of
// what it says.
func TestRedactError(t *testing.T) {
	for in, want := range map[string]string{
		"hass: GET /local/x.png?token=secret: 404 Not Found":                "hass: GET /local/x.png: 404 Not Found",
		`Get "http://h:1/x.png?token=secret": dial tcp: connection refused`: `Get "http://h:1/x.png": dial tcp: connection refused`,
		"hass: GET /local/x.png#secret: 404 Not Found":                      "hass: GET /local/x.png: 404 Not Found",
		"hass: GET /local/x.png: 404 Not Found":                             "hass: GET /local/x.png: 404 Not Found",
		"picture: reading the image header: image: unknown format":          "picture: reading the image header: image: unknown format",
		"hass: GET /a?t=1: 500 then GET /b?u=2: 404":                        "hass: GET /a: 500 then GET /b: 404",
		"hass: GET http://user:p@ss@h/x: 401 Unauthorized":                  "hass: GET http://h/x: 401 Unauthorized",
		"hass: GET http://user:pass@h/x: 401 Unauthorized":                  "hass: GET http://h/x: 401 Unauthorized",
		`Get "http://user:***@h:1/x.png?k=v": dial tcp: refused`:            `Get "http://h:1/x.png": dial tcp: refused`,
	} {
		if got := redactError(in); got != want {
			t.Errorf("redactError(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

// ShowPicture refuses what it cannot show, and shows nothing for it.
func TestShowPictureRefusesWhatItCannotShow(t *testing.T) {
	f := newFeature(t)
	if err := f.ShowPicture("front door", Caption{ID: "1", Message: "x"}, time.Minute, ""); err == nil {
		t.Error("took a picture that is no kind of source")
	}
	if _, up := f.Camera(); up {
		t.Error("a refused picture put the camera page up")
	}
}

// A camera asked for any other way is a plain camera: the caption does not carry over.
func TestShowCameraClearsACaption(t *testing.T) {
	fakePictures(t)
	f := newFeature(t)
	if err := f.ShowPicture("/local/door.png", Caption{ID: "1", Message: "x"}, time.Minute, ""); err != nil {
		t.Fatal(err)
	}
	f.ShowCamera("camera.deck", time.Minute)
	if v, up := f.Camera(); !up || v.Caption != nil || v.Entity != "camera.deck" {
		t.Errorf("view %+v, want camera.deck with no caption", v)
	}
	if f.PictureUp("1") {
		t.Error("the notification's view is still counted as up")
	}
}

// A still does not take the camera page from a camera somebody is watching (a doorbell's): it waits for
// that view to end, counted as up meanwhile, and then comes up in its place, the camera (and so its
// sound, which ends when the page's entity changes) gone.
func TestAStillWaitsForACameraView(t *testing.T) {
	fakePictures(t)
	saved := waitingPoll
	t.Cleanup(func() { waitingPoll = saved })
	waitingPoll = 5 * time.Millisecond
	f := newFeature(t)
	f.ShowCamera("camera.deck", time.Minute)
	if err := f.ShowPicture("/local/door.png", Caption{ID: "1", Message: "x"}, time.Minute, ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if v, up := f.Camera(); !up || v.Entity != "camera.deck" || v.Caption != nil {
		t.Fatalf("view %+v, want the deck camera still up, untouched", v)
	}
	if !f.PictureUp("1") {
		t.Error("the waiting picture's notification is not counted as up, so it would be reported over")
	}
	v, _ := f.Camera()
	f.HideView(v.View)
	eventually(t, "the picture after the camera", func() bool {
		v, up := f.Camera()
		return up && v.Entity == "/local/door.png" && v.Caption != nil && v.Caption.ID == "1"
	})
	if f.cameraViewUp("camera.deck") {
		t.Error("the deck camera still counts as up, so its sound would play on")
	}
}

// A still that waits longer than it was to be up never comes up, and one taken down while it waits
// does not either.
func TestAWaitingStillCanEndBeforeItShows(t *testing.T) {
	fakePictures(t)
	saved := waitingPoll
	t.Cleanup(func() { waitingPoll = saved })
	waitingPoll = 5 * time.Millisecond
	f := newFeature(t)
	f.ShowCamera("camera.deck", time.Minute)
	_ = f.ShowPicture("/local/door.png", Caption{ID: "1"}, 30*time.Millisecond, "")
	time.Sleep(60 * time.Millisecond)
	if f.PictureUp("1") {
		t.Error("a still whose time ran out while it waited is still counted as up")
	}
	_ = f.ShowPicture("/local/door.png", Caption{ID: "2"}, time.Minute, "")
	if !f.HidePicture("2") || f.PictureUp("2") {
		t.Error("a waiting still was not taken down")
	}
	v, _ := f.Camera()
	f.HideView(v.View)
	time.Sleep(60 * time.Millisecond)
	if v, up := f.Camera(); up {
		t.Errorf("view %+v came up after the camera, from a still that had ended", v)
	}
}

// A notification's picture of the camera already up (a doorbell's own) does not cut that view short,
// and is not held back at night: the camera was up anyway.
func TestAPictureOfTheCameraUpKeepsItsTime(t *testing.T) {
	fakePictures(t)
	f := newFeature(t)
	f.ShowCamera("camera.front_door", 5*time.Minute)
	before, _ := f.Camera()
	if err := f.ShowPicture("camera.front_door", Caption{ID: "3", Title: "Doorbell"}, 30*time.Second, ""); err != nil {
		t.Fatal(err)
	}
	v, up := f.Camera()
	if !up || v.Caption == nil || v.Until.Before(before.Until) {
		t.Errorf("view until %v (was %v), captioned %v: the doorbell's view was cut short", v.Until, before.Until, v.Caption != nil)
	}
	if !v.OverPlain {
		t.Error("a picture over a camera that was up is not marked so")
	}
	// On its own, a camera notification is not over anything.
	f.HideCamera()
	_ = f.ShowPicture("camera.front_door", Caption{ID: "4"}, 30*time.Second, "")
	if v, _ := f.Camera(); v.OverPlain {
		t.Error("a picture put up on its own is marked as over a camera")
	}
}

// How a notification's view went is kept for notify to report: replaced by a camera, dismissed by a
// tap; nothing when it ran out.
func TestHowAPictureWentIsKept(t *testing.T) {
	fakePictures(t)
	f := newFeature(t)
	_ = f.ShowPicture("camera.front_door", Caption{ID: "5"}, time.Minute, "")
	f.ShowCamera("camera.deck", time.Minute)
	if got := f.PictureEnded("5"); got != "replaced" {
		t.Errorf("a picture a camera took the page from ended %q, want replaced", got)
	}
	_ = f.ShowPicture("camera.front_door", Caption{ID: "6"}, time.Minute, "")
	v, _ := f.Camera()
	f.HideView(v.View)
	if got := f.PictureEnded("6"); got != "dismissed" {
		t.Errorf("a picture tapped away ended %q, want dismissed", got)
	}
	_ = f.ShowPicture("camera.front_door", Caption{ID: "7"}, 20*time.Millisecond, "")
	time.Sleep(40 * time.Millisecond)
	if got := f.PictureEnded("7"); got != "" {
		t.Errorf("a picture that ran out ended %q, want nothing", got)
	}
}

// A still waiting for a camera view that goes before it shows is reported the same way: dismissed when
// taken down, replaced by a newer notification's picture, waiting or a camera; nothing when its time
// ran out while it waited.
func TestHowAWaitingStillWentIsKept(t *testing.T) {
	fakePictures(t)
	saved := waitingPoll
	t.Cleanup(func() { waitingPoll = saved })
	waitingPoll = time.Hour // nothing comes up from waiting during the test
	f := newFeature(t)
	f.ShowCamera("camera.deck", time.Minute)

	_ = f.ShowPicture("/local/door.png", Caption{ID: "11"}, time.Minute, "")
	f.HidePicture("11")
	if got := f.PictureEnded("11"); got != "dismissed" {
		t.Errorf("a waiting still taken down ended %q, want dismissed", got)
	}

	_ = f.ShowPicture("/local/door.png", Caption{ID: "12"}, time.Minute, "")
	_ = f.ShowPicture("/local/door.png", Caption{ID: "13"}, time.Minute, "")
	if got := f.PictureEnded("12"); got != "replaced" {
		t.Errorf("a waiting still another took the place of ended %q, want replaced", got)
	}

	_ = f.ShowPicture("camera.front_door", Caption{ID: "14"}, time.Minute, "")
	if got := f.PictureEnded("13"); got != "replaced" {
		t.Errorf("a waiting still a camera notification replaced ended %q, want replaced", got)
	}

	f.HideCamera()
	f.ShowCamera("camera.deck", time.Minute)
	_ = f.ShowPicture("/local/door.png", Caption{ID: "15"}, 20*time.Millisecond, "")
	time.Sleep(40 * time.Millisecond)
	_ = f.ShowPicture("/local/door.png", Caption{ID: "16"}, time.Minute, "")
	if got := f.PictureEnded("15"); got != "" {
		t.Errorf("a waiting still whose time ran out ended %q, want nothing", got)
	}
	f.HidePicture("16")
}

// What waits with a still for the camera view ends as soon as the still is taken down, replaced by a
// camera notification, or dropped as the device shuts down, not at its next look, an hour on here.
func TestAWaitingStillStopsWaitingAtOnce(t *testing.T) {
	fakePictures(t)
	saved := waitingPoll
	t.Cleanup(func() { waitingPoll = saved })
	waitingPoll = time.Hour
	for _, c := range []struct {
		name string
		end  func(f *Feature)
	}{
		{"taken down", func(f *Feature) { f.HidePicture("21") }},
		{"replaced by a camera notification", func(f *Feature) {
			_ = f.ShowPicture("camera.front_door", Caption{ID: "22"}, time.Minute, "")
		}},
		{"shutting down", func(f *Feature) { f.stopWaiting() }},
	} {
		f := &Feature{}
		f.ShowCamera("camera.deck", time.Minute)
		_ = f.ShowPicture("/local/door.png", Caption{ID: "21"}, time.Minute, "")
		c.end(f)
		f.HideCamera()
		ended := make(chan struct{})
		go func() { f.loops.Wait(); close(ended) }()
		select {
		case <-ended:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: the wait for the camera view went on", c.name)
		}
	}
}

// A camera named by a notification is live, with its caption.
func TestACameraPictureIsLive(t *testing.T) {
	fakePictures(t)
	f := newFeature(t)
	if err := f.ShowPicture("camera.front_door", Caption{ID: "7", Title: "Doorbell", Message: "x"}, time.Minute, ""); err != nil {
		t.Fatal(err)
	}
	v, up := f.Camera()
	if !up || v.Entity != "camera.front_door" || v.Caption == nil || v.Caption.ID != "7" {
		t.Errorf("view %+v, want camera.front_door captioned", v)
	}
}
