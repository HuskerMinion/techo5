package home

import (
	"bytes"
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
	f := &Feature{}
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

// A picture that cannot be fetched still comes up, with its caption and why there is no picture.
func TestAStillThatFailsKeepsItsCaption(t *testing.T) {
	fakePictures(t)
	f := &Feature{}
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
		"/local/door.png":                     "/local/door.png",
		"/local/door.png?token=secret":        "/local/door.png",
		"https://x.test/a.jpg?sig=1&b=2#frag": "https://x.test/a.jpg",
		"https://x.test/a.jpg#frag?not-query": "https://x.test/a.jpg",
		"camera.front_door":                   "camera.front_door",
		"":                                    "",
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
		"http://127.0.0.1:1/missing.png?token=secret", // nothing listens there: the error is the dial's
	} {
		f := &Feature{}
		if err := f.ShowPicture(src, Caption{ID: "1", Message: "x"}, time.Minute, ""); err != nil {
			t.Fatal(err)
		}
		eventually(t, "the error for "+src, func() bool { v, _ := f.Camera(); return v.Error != "" })
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
	} {
		if got := redactError(in); got != want {
			t.Errorf("redactError(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

// ShowPicture refuses what it cannot show, and shows nothing for it.
func TestShowPictureRefusesWhatItCannotShow(t *testing.T) {
	f := &Feature{}
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
	f := &Feature{}
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

// A picture notification takes the camera page from the camera on it, so a camera's sound (which
// ends when the page's entity changes) ends with it.
func TestAPictureReplacesACameraView(t *testing.T) {
	fakePictures(t)
	f := &Feature{}
	f.ShowCamera("camera.deck", time.Minute)
	if err := f.ShowPicture("/local/door.png", Caption{ID: "1", Message: "x"}, time.Minute, ""); err != nil {
		t.Fatal(err)
	}
	if v, _ := f.Camera(); v.Entity != "/local/door.png" || v.Caption == nil {
		t.Errorf("view %+v, want the picture with its caption", v)
	}
	if f.cameraViewUp("camera.deck") {
		t.Error("the deck camera still counts as up, so its sound would play on")
	}
}

// A camera named by a notification is live, with its caption.
func TestACameraPictureIsLive(t *testing.T) {
	fakePictures(t)
	f := &Feature{}
	if err := f.ShowPicture("camera.front_door", Caption{ID: "7", Title: "Doorbell", Message: "x"}, time.Minute, ""); err != nil {
		t.Fatal(err)
	}
	v, up := f.Camera()
	if !up || v.Entity != "camera.front_door" || v.Caption == nil || v.Caption.ID != "7" {
		t.Errorf("view %+v, want camera.front_door captioned", v)
	}
}
