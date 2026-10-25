package home

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// A server that keeps saying there is another page, of nothing the slideshow can show, is not asked
// for page after page: a page that adds nothing ends the gathering.
func TestImmichPagesThatAddNothingEnd(t *testing.T) {
	letLoopback(t)
	asked := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		fmt.Fprint(w, `{"assets":{"total":1,"count":1,"items":[{"id":"0a0a0a0a-0000-4000-8000-000000000001","type":"VIDEO"}],"nextPage":"2"}}`)
	}))
	t.Cleanup(srv.Close)
	photos, err := immichPhotos(context.Background(), config.Immich{Server: srv.URL, Key: testKey}, ImmichAll, immichFilter{}, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(photos) != 0 || asked != 1 {
		t.Errorf("%d photos after %d pages, want none after one", len(photos), asked)
	}
}

// Only the whole library, or an album by an id of Immich's form, is a source.
func TestImmichSourceOK(t *testing.T) {
	for source, want := range map[string]bool{
		ImmichAll:                     true,
		ImmichAlbumPrefix + albumA:    true,
		ImmichAlbumPrefix:             false,
		ImmichAlbumPrefix + "../x":    false,
		ImmichAlbumPrefix + "a b":     false,
		"media-source://media_source": false,
	} {
		if got := ImmichSourceOK(source); got != want {
			t.Errorf("ImmichSourceOK(%q) = %v, want %v", source, got, want)
		}
	}
}

// An album's kept name is shown for that album only, not for another chosen without its name known.
func TestImmichAlbumNameIsTheAlbums(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	immichNames.remember(nil)
	other := "0b0b0b0b-0000-4000-8000-000000000002"
	if err := config.Set().Home().Immich(config.Immich{Album: "Trip", AlbumID: albumA}); err != nil {
		t.Fatal(err)
	}
	if got := ImmichSourceName(ImmichAlbumPrefix + albumA); got != "Trip" {
		t.Errorf("the album named: %q", got)
	}
	if got := ImmichSourceName(ImmichAlbumPrefix + other); got != "Immich album" {
		t.Errorf("another album named %q", got)
	}
}

// A look whose photos all failed counts toward the screen saying so, unless Immich had merely no
// preview of them yet.
func TestFetchCounts(t *testing.T) {
	if fetchCounts(nil) {
		t.Error("no error counted")
	}
	if !fetchCounts(errors.New("immich at host could not be reached")) {
		t.Error("an unreachable server not counted")
	}
	if fetchCounts(fmt.Errorf("immich at host: %w (no preview yet, or the photo was removed)", errImmichGone)) {
		t.Error("a photo with no preview yet counted")
	}
}

// A new server: an album of the one before is not this one's, so the slideshow shows the whole library.
func TestResetImmichLeavesTheOldAlbum(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	f := Get()
	f.SetSlideshowSource(ImmichAlbumPrefix + albumA)
	f.ResetImmich()
	if source, _, _ := f.SlideshowSettings(); source != ImmichAll {
		t.Errorf("source after a new server: %q", source)
	}
	f.SetSlideshowSource(LocalPhotos)
	f.ResetImmich()
	if source, _, _ := f.SlideshowSettings(); source != LocalPhotos {
		t.Errorf("a source not Immich's changed: %q", source)
	}
}

// A list gathered for the source before a change is not kept for the source after it.
func TestGatheredForAnotherSourceIsDropped(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	f := Get()
	f.mu.Lock()
	gen := f.slideshowGen
	f.mu.Unlock()
	f.ResetImmich()
	src := config.Get().Home.Slideshow.Source
	if f.keepGathered(gen, src, []hass.Media{{ID: "old"}}, false) {
		t.Error("a list gathered before the change was kept")
	}
	f.mu.Lock()
	gen = f.slideshowGen
	f.mu.Unlock()
	if f.keepGathered(gen, src+"-another", []hass.Media{{ID: "other"}}, false) {
		t.Error("a list gathered for another source was kept")
	}
	if !f.keepGathered(gen, src, []hass.Media{{ID: "new"}}, false) {
		t.Error("a list gathered after the change was dropped")
	}
}

// An album chosen before the album's id was kept with its name is named at start, from Immich.
func TestAnAlbumIsNamedAtStart(t *testing.T) {
	letLoopback(t)
	was := nameAgainAfter
	nameAgainAfter = 10 * time.Millisecond
	t.Cleanup(func() { naming.Wait(); nameAgainAfter = was }) // nothing left running into the next test
	srv, _ := fakeImmich(t)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Home().Immich(config.Immich{Server: srv.URL, Key: testKey, Album: "Old name"}); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Home().Slideshow(config.Slideshow{Source: ImmichAlbumPrefix + albumA}); err != nil {
		t.Fatal(err)
	}
	Get().Restore(config.Get())
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if i := config.Get().Home.Immich; i.AlbumID == albumA && i.Album == "Trip" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("not named at start: %+v", config.Get().Home.Immich)
}
