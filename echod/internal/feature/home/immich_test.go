package home

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/netguard"
)

// testKey is a made-up API key; the fake refuses any other.
const testKey = "made-up-key-for-tests-0123456789"

const (
	albumA = "11111111-1111-1111-1111-111111111111"
	photo1 = "aaaaaaaa-0000-0000-0000-000000000001"
	photo2 = "aaaaaaaa-0000-0000-0000-000000000002"
	video1 = "aaaaaaaa-0000-0000-0000-0000000000f1"
	photo3 = "aaaaaaaa-0000-0000-0000-000000000003"
)

// letLoopback lets the fake, on the loopback, through netguard for the length of a test.
func letLoopback(t *testing.T) {
	was := netguard.Refused
	netguard.Refused = func(ip net.IP) bool { return !ip.IsLoopback() && was(ip) }
	t.Cleanup(func() { netguard.Refused = was })
}

// fakeImmich is an Immich that answers as v3 does: albums, two pages of search, a preview.
// noPreview is a photo the fake Immich has no preview of.
const noPreview = "0f0f0f0f-0000-4000-8000-000000000000"

func fakeImmich(t *testing.T) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var searches []map[string]any
	pic := immichJPEG(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != testKey {
			http.Error(w, `{"message":"Invalid API key"}`, http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/albums":
			fmt.Fprintf(w, `[{"id":%q,"albumName":"Trip","assetCount":3},{"id":"../../etc","albumName":"Bad","assetCount":1}]`, albumA)
		case r.Method == http.MethodPost && r.URL.Path == "/api/search/metadata":
			var q map[string]any
			_ = json.NewDecoder(r.Body).Decode(&q)
			searches = append(searches, q)
			if q["page"] == float64(1) {
				fmt.Fprintf(w, `{"assets":{"total":3,"count":2,"items":[{"id":%q,"type":"IMAGE"},{"id":%q,"type":"VIDEO"}],"nextPage":"2"}}`, photo1, video1)
				return
			}
			fmt.Fprintf(w, `{"assets":{"total":3,"count":2,"items":[{"id":%q,"type":"IMAGE"},{"id":%q,"type":"IMAGE"}],"nextPage":null}}`, photo2, photo3)
		case r.Method == http.MethodGet && r.URL.Path == "/api/assets/"+noPreview+"/thumbnail":
			http.NotFound(w, r) // a photo Immich has not made a preview of yet
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/assets/") && r.URL.Query().Get("size") == "preview":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(pic)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &searches
}

func immichJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for i := range img.Pix {
		img.Pix[i] = byte(i)
	}
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// Albums come back with ids Immich would give; anything else is dropped, and the names are kept.
func TestImmichAlbums(t *testing.T) {
	letLoopback(t)
	srv, _ := fakeImmich(t)
	albums, err := ImmichAlbums(context.Background(), config.Immich{Server: srv.URL, Key: testKey})
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 1 || albums[0].ID != albumA || albums[0].Name != "Trip" || albums[0].Photos != 3 {
		t.Fatalf("albums = %+v", albums)
	}
	if name := ImmichSourceName(ImmichAlbumPrefix + albumA); name != "Trip" {
		t.Errorf("name = %q", name)
	}
}

// A wrong key is ErrImmichKey, and no error says the key or the whole address.
func TestImmichWrongKey(t *testing.T) {
	letLoopback(t)
	srv, _ := fakeImmich(t)
	_, err := ImmichAlbums(context.Background(), config.Immich{Server: srv.URL, Key: "not-it"})
	if !errors.Is(err, ErrImmichKey) {
		t.Fatalf("err = %v", err)
	}
	for _, s := range []string{"not-it", "/api/"} {
		if strings.Contains(err.Error(), s) {
			t.Errorf("the error carries %q: %v", s, err)
		}
	}
}

// Photos are gathered page by page, videos left out, an album asked for by its id and the whole
// library with none; max stops the gathering.
func TestImmichPhotos(t *testing.T) {
	letLoopback(t)
	srv, searches := fakeImmich(t)
	s := config.Immich{Server: srv.URL, Key: testKey}
	got, err := immichPhotos(context.Background(), s, ImmichAlbumPrefix+albumA, immichFilter{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range got {
		ids = append(ids, m.ID)
		if !m.CanPlay || !isPhoto(m) {
			t.Errorf("%s is not shown as a photo", m.ID)
		}
	}
	want := []string{immichAssetPrefix + photo1, immichAssetPrefix + photo2, immichAssetPrefix + photo3}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Errorf("photos = %v, want %v", ids, want)
	}
	if len(*searches) != 2 || (*searches)[0]["type"] != "IMAGE" || fmt.Sprint((*searches)[0]["albumIds"]) != "["+albumA+"]" {
		t.Errorf("searches = %v", *searches)
	}

	*searches = nil
	got, err = immichPhotos(context.Background(), s, ImmichAll, immichFilter{}, 1)
	if err != nil || len(got) != 1 || len(*searches) != 1 {
		t.Errorf("whole library, max 1: %d photos, %d searches, err %v", len(got), len(*searches), err)
	}
	if _, ok := (*searches)[0]["albumIds"]; ok {
		t.Error("the whole library was asked for with an album")
	}
	if _, err := immichPhotos(context.Background(), s, ImmichAlbumPrefix+"../x", immichFilter{}, 10); err == nil {
		t.Error("an album id with a path in it was asked for")
	}
}

// A photo is Immich's preview, fetched by its id; an id that is not one is refused before any request.
func TestImmichPhoto(t *testing.T) {
	letLoopback(t)
	srv, _ := fakeImmich(t)
	s := config.Immich{Server: srv.URL, Key: testKey}
	b, err := immichPhoto(context.Background(), s, immichAssetPrefix+photo1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeWithin(b, maxPhotoPixels, "test", 0, 0); err != nil {
		t.Errorf("the preview does not decode: %v", err)
	}
	if _, err := immichPhoto(context.Background(), s, immichAssetPrefix+"x/../../albums"); err == nil {
		t.Error("a photo id with a path in it was fetched")
	}
	// A photo Immich has no preview of yet (a library just added) is said as that, not as an album gone.
	_, err = immichPhoto(context.Background(), s, immichAssetPrefix+noPreview)
	if !errors.Is(err, errImmichGone) || !strings.Contains(err.Error(), "no preview yet") || strings.Contains(err.Error(), "album") {
		t.Errorf("a photo with no preview: %v", err)
	}
}

// What the device dials goes through netguard: the loopback, unless a test lets it, is refused.
func TestImmichGuarded(t *testing.T) {
	srv, _ := fakeImmich(t)
	_, err := ImmichAlbums(context.Background(), config.Immich{Server: srv.URL, Key: testKey})
	if err == nil || !strings.Contains(err.Error(), "could not be reached") {
		t.Errorf("the loopback was reached: %v", err)
	}
}

// A reply past its cap, a server that does not answer in time, and a redirect are all errors; the
// redirect is not followed, so the key's header never goes where it pointed.
func TestImmichLimits(t *testing.T) {
	letLoopback(t)
	var elsewhere atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { elsewhere.Add(1) }))
	defer other.Close()
	big := strings.Repeat("x", immichListMax+10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/albums":
			_, _ = w.Write([]byte(big))
		case "/api/search/metadata":
			time.Sleep(300 * time.Millisecond)
		default:
			http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
		}
	}))
	defer srv.Close()
	s := config.Immich{Server: srv.URL, Key: testKey}
	if _, err := ImmichAlbums(context.Background(), s); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Errorf("oversize: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := immichPhotos(ctx, s, ImmichAll, immichFilter{}, 10); err == nil || !strings.Contains(err.Error(), "in time") {
		t.Errorf("slow: %v", err)
	}
	if _, err := immichPhoto(context.Background(), s, immichAssetPrefix+photo1); err == nil {
		t.Error("a redirect was taken for a photo")
	}
	if elsewhere.Load() != 0 {
		t.Error("the redirect was followed")
	}
}

// Only an http or https address with a host and nothing else in it is kept.
func TestCheckImmichServer(t *testing.T) {
	for in, want := range map[string]string{
		"http://192.168.1.20:2283/":    "http://192.168.1.20:2283",
		" https://photos.example.org ": "https://photos.example.org",
		"https://example.org/immich":   "https://example.org/immich",
	} {
		if got, err := CheckImmichServer(in); err != nil || got != want {
			t.Errorf("%q: %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "192.168.1.20:2283", "ftp://x", "http://user:pw@x", "http://x/?k=1", "http://x/#a", "file:///etc"} {
		if _, err := CheckImmichServer(in); err == nil {
			t.Errorf("%q was taken", in)
		}
	}
}

// End to end: an Immich source is gathered and its photos fetched and fitted, with no Home Assistant.
func TestImmichSlideshow(t *testing.T) {
	letLoopback(t)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	srv, _ := fakeImmich(t)
	if err := config.Set().Home().Immich(config.Immich{Server: srv.URL, Key: testKey}); err != nil {
		t.Fatal(err)
	}
	photos, err := gatherSlideshow(config.Slideshow{Source: ImmichAlbumPrefix + albumA})
	if err != nil || len(photos) != 3 {
		t.Fatalf("gathered %d, %v", len(photos), err)
	}
	img, err := fetchSlideshowImage(photos[0].ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != slideshowW || img.Bounds().Dy() != slideshowH {
		t.Errorf("fitted to %v", img.Bounds())
	}
	if got := slideshowFolderName(ImmichAll); got != "Immich" {
		t.Errorf("whole library named %q", got)
	}

	if err := config.Set().Home().Immich(config.Immich{}); err != nil {
		t.Fatal(err)
	}
	if _, err := gatherSlideshow(config.Slideshow{Source: ImmichAll}); err == nil {
		t.Error("gathered from an Immich that was forgotten")
	}
}
