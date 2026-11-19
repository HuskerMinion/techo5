package home

import (
	"fmt"
	"image"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// A picture this screen cannot decode (an iPhone's HEIC itself) is not the source's trouble, and is
// skipped from then on; a folder of nothing else says so.
func TestUndecodablePhotosAreSkipped(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if fetchCounts(fmt.Errorf("slideshow photo x: reading the image header: %w", image.ErrFormat)) {
		t.Error("a picture this screen does not decode counted as the source failing")
	}
	f := &Feature{}
	f.slideshow.children = []hass.Media{{ID: "a"}, {ID: "b"}}
	f.slideshow.browsed = time.Now()
	f.slideshow.undecodable = map[string]bool{"a": true}
	h := config.Slideshow{InOrder: true}
	for range 3 {
		if id, ok := f.nextSlideshowChild(h); !ok || id != "b" {
			t.Fatalf("picked %q, %v; want b, past a", id, ok)
		}
	}
	f.slideshow.undecodable["b"] = true
	for range slideshowGiveUp {
		if id, ok := f.nextSlideshowChild(h); ok {
			t.Fatalf("picked %q, which this screen cannot decode", id)
		}
	}
	if got := f.slideshow.trouble; got != slideshowNoneShown {
		t.Errorf("the screen says %q, want %q", got, slideshowNoneShown)
	}
}
