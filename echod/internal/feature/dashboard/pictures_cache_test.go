//go:build !dot

package dashboard

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// solid is a small PNG of one color: pictures told apart by their bytes.
func solid(t *testing.T, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// servePictures is Home Assistant answering with h, the device's access set to it.
func servePictures(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	was := hass.Path
	hass.Path = filepath.Join(t.TempDir(), "hass.json")
	t.Cleanup(func() { hass.Path = was })
	if err := hass.Get().Set(srv.URL, "token"); err != nil {
		t.Fatal(err)
	}
}

// quickPictures makes the floor and the look-over short enough for a test to see them.
func quickPictures(t *testing.T) {
	t.Helper()
	floor, tick := pictureFloor, pictureTick
	pictureFloor, pictureTick = 150*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { pictureFloor, pictureTick = floor, tick })
}

var red, blue = color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}

// A picture is kept as its server's Cache-Control says: no-store and no-cache fetched again on the
// next redraw, a max-age for its seconds between the floor and ten minutes, and nothing said (or
// nothing understood) for the ten minutes it always was.
func TestAPicturesServerSaysHowLongItIsKept(t *testing.T) {
	pic := solid(t, red)
	var header atomic.Value
	servePictures(t, func(w http.ResponseWriter, r *http.Request) {
		if h := header.Load().(string); h != "" {
			w.Header().Set("Cache-Control", h)
		}
		_, _ = w.Write(pic)
	})
	for _, c := range []struct {
		header   string
		wait     time.Duration
		onRedraw bool
		noStore  bool
	}{
		{"", pictureEvery, false, false},
		{"no-store", pictureEvery, true, true},
		{"no-cache", pictureEvery, true, false},
		{"max-age=0", pictureEvery, true, false},
		{"max-age=30", 30 * time.Second, false, false},
		{"private, max-age=60", time.Minute, false, false},
		{"max-age=1", pictureFloor, false, false},
		{"max-age=86400", pictureEvery, false, false},
		{"max-age=soon", pictureEvery, false, false},
		{"public, must-revalidate", pictureEvery, false, false},
	} {
		header.Store(c.header)
		sh, k, same, err := fetchPicture(context.Background(), wanted{url: "/local/a.png"}, keeping{})
		if err != nil || sh.img == nil || same {
			t.Fatalf("%q: %v, %v, %v", c.header, sh.img != nil, same, err)
		}
		wait, onRedraw := keepFor(k)
		if wait != c.wait || onRedraw != c.onRedraw || sh.noStore != c.noStore {
			t.Errorf("%q: kept %v, on redraw %v, no-store %v; want %v, %v, %v",
				c.header, wait, onRedraw, sh.noStore, c.wait, c.onRedraw, c.noStore)
		}
	}
}

// A picture with an ETag or a Last-Modified is asked about with them, and a 304 keeps the bytes it
// has, not decoded or drawn again; once it changes, the new one is fetched whole.
func TestAPictureIsRevalidatedWith304(t *testing.T) {
	for _, by := range []string{"ETag", "Last-Modified"} {
		t.Run(by, func(t *testing.T) {
			var mu sync.Mutex
			version, asked, notModified := "v1", "", 0
			pics := map[string][]byte{"v1": solid(t, red), "v2": solid(t, blue)}
			servePictures(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				tag := `"` + version + `"`
				when := "Mon, 02 Jan 2026 15:04:05 GMT"
				if version == "v2" {
					when = "Tue, 03 Jan 2026 15:04:05 GMT"
				}
				w.Header().Set("Cache-Control", "no-cache")
				if by == "ETag" {
					asked = r.Header.Get("If-None-Match")
					w.Header().Set("ETag", tag)
					if asked == tag {
						notModified++
						w.WriteHeader(http.StatusNotModified)
						return
					}
				} else {
					asked = r.Header.Get("If-Modified-Since")
					w.Header().Set("Last-Modified", when)
					if asked == when {
						notModified++
						w.WriteHeader(http.StatusNotModified)
						return
					}
				}
				_, _ = w.Write(pics[version])
			})
			want := wanted{url: "/local/a.png"}
			sh, k, same, err := fetchPicture(context.Background(), want, keeping{})
			if err != nil || sh.img == nil || same || asked != "" {
				t.Fatalf("first: %v, %v, %v, asked with %q", sh.img != nil, same, err, asked)
			}
			sh, k, same, err = fetchPicture(context.Background(), want, k)
			if err != nil || !same || sh.img != nil || asked == "" || notModified != 1 {
				t.Fatalf("again: same %v, %v, asked with %q, %d 304s", same, err, asked, notModified)
			}
			if _, onRedraw := keepFor(k); !onRedraw {
				t.Errorf("after a 304 it is no longer asked about on a redraw")
			}
			mu.Lock()
			version = "v2"
			mu.Unlock()
			sh, _, same, err = fetchPicture(context.Background(), want, k)
			if err != nil || same || sh.img == nil || notModified != 1 {
				t.Fatalf("changed: same %v, %v, %d 304s", same, err, notModified)
			}
			if got := sh.img.At(0, 0); got != (color.RGBA{0, 0, 255, 255}) && got != (color.NRGBA{0, 0, 255, 255}) {
				t.Errorf("changed: the picture is %v, want the new one", got)
			}
		})
	}
}

// The same bytes sent again by a server with no validators are not decoded or drawn again.
func TestTheSameBytesAreNotDrawnAgain(t *testing.T) {
	pic := solid(t, red)
	servePictures(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(pic)
	})
	want := wanted{url: "/local/a.png"}
	_, k, _, err := fetchPicture(context.Background(), want, keeping{})
	if err != nil {
		t.Fatal(err)
	}
	if sh, _, same, err := fetchPicture(context.Background(), want, k); err != nil || !same || sh.img != nil {
		t.Errorf("the same bytes: same %v, decoded %v, %v", same, sh.img != nil, err)
	}
}

// A no-store picture is fetched again on the page's next redraw, not before (an idle page doesn't
// poll it), and never sooner than the floor after the last fetch, however often the page is redrawn.
func TestANoStorePictureIsFetchedOnRedrawNoSoonerThanTheFloor(t *testing.T) {
	quickPictures(t)
	var hits atomic.Int32
	pics := [][]byte{solid(t, red), solid(t, blue)}
	servePictures(t, func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(pics[n%2]) // a new picture each time
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	redraws := make(chan struct{}, 1)
	var gots atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		keepPictures(ctx, []wanted{{url: "/local/snap.png"}}, redraws, func(string, shot) { gots.Add(1) })
	}()
	eventually(t, "the first fetch", func() bool { return gots.Load() == 1 })

	time.Sleep(3 * pictureFloor)
	if n := hits.Load(); n != 1 {
		t.Fatalf("with no redraw: fetched %d times, want once", n)
	}

	redraws <- struct{}{}
	eventually(t, "the fetch on a redraw", func() bool { return gots.Load() == 2 })

	// Redrawn all the time: fetched once a floor at most.
	start, before := time.Now(), hits.Load()
	for time.Since(start) < 6*pictureFloor {
		select {
		case redraws <- struct{}{}:
		default:
		}
		time.Sleep(5 * time.Millisecond)
	}
	got := hits.Load() - before
	if most := int32(time.Since(start)/pictureFloor) + 1; got > most || got < 2 {
		t.Errorf("redrawn for %v: fetched %d times, want 2 to %d", time.Since(start), got, most)
	}
	cancel()
	<-done
}

// A picture that changes on its server redraws its card on the page; one fetched again and the same
// leaves the page as it is.
func TestAChangedPictureRedrawsTheCard(t *testing.T) {
	quickPictures(t)
	var mu sync.Mutex
	pic := solid(t, red)
	var hits atomic.Int32
	servePictures(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Cache-Control", "max-age=1") // fetched again each second
		_, _ = w.Write(pic)
	})
	f := drawnAt(t, "home/snap")
	snap := wanted{url: "/local/snap.png"}
	s := loaded(f, snap)
	color0 := func() color.Color {
		d := f.Drawn(screenWidth)
		if len(d.Sections) == 0 || len(d.Sections[0].Blocks) == 0 || d.Sections[0].Blocks[0].Picture.Image == nil {
			return nil
		}
		return d.Sections[0].Blocks[0].Picture.Image.At(0, 0)
	}
	isColor := func(c color.Color, want color.RGBA) bool {
		if c == nil {
			return false
		}
		r, g, b, _ := c.RGBA()
		wr, wg, wb, _ := want.RGBA()
		return r == wr && g == wg && b == wb
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		keepPictures(ctx, []wanted{snap}, s.redraws, s.got)
	}()
	eventually(t, "the picture", func() bool { return isColor(color0(), red) })

	// Fetched again and the same: the page isn't rebuilt for it.
	eventually(t, "a second fetch", func() bool { return hits.Load() >= 3 })
	time.Sleep(2 * publishEvery)
	version := f.Drawn(screenWidth).Version
	at := hits.Load()
	eventually(t, "another fetch", func() bool { return hits.Load() > at })
	time.Sleep(2 * publishEvery)
	if v := f.Drawn(screenWidth).Version; v != version {
		t.Errorf("the same picture rebuilt the page: version %d, was %d", v, version)
	}

	mu.Lock()
	pic = solid(t, blue)
	mu.Unlock()
	eventually(t, "the changed picture on its card", func() bool { return isColor(color0(), blue) })
	if v := f.Drawn(screenWidth).Version; v == version {
		t.Errorf("the changed picture did not rebuild the page")
	}
	cancel()
	<-done
}

// eventually waits up to five seconds for ok, failing the test as what it was waiting for.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("no %s", what)
}

// A no-store picture is drawn on its page and not kept for the next.
func TestANoStorePictureIsNotKeptForTheNextPage(t *testing.T) {
	var r recent
	r.put(0, "snap", shot{img: image.NewRGBA(image.Rect(0, 0, 4, 4)), noStore: true})
	if _, ok := r.get("snap"); ok {
		t.Errorf("a no-store picture was kept between pages")
	}
}
