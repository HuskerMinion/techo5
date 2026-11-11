//go:build !dot

package display

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The photos' rows offer pairs where the screen has halves, the date for every source (#502), and
// Immich's own rows only for an Immich source.
func TestImmichPhotoRows(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	ids := func() map[string]bool {
		out := map[string]bool{}
		for _, r := range slideshowRows(false) {
			out[r.id] = true
		}
		return out
	}
	if err := config.Set().Home().Slideshow(config.Slideshow{Mode: config.SlideshowScreensaver, Source: home.LocalPhotos}); err != nil {
		t.Fatal(err)
	}
	got := ids()
	if !got["photocaption"] || got["immichfav"] {
		t.Errorf("photos on the device: %v, want the date and none of Immich's rows", got)
	}
	if got["photopairs"] != home.PairsHere() {
		t.Errorf("photo pairs offered %v on a screen where they are %v", got["photopairs"], home.PairsHere())
	}
	if err := config.Set().Home().Slideshow(config.Slideshow{Mode: config.SlideshowScreensaver, Source: home.ImmichAll}); err != nil {
		t.Fatal(err)
	}
	got = ids()
	if !got["photocaption"] || !got["immichfav"] {
		t.Errorf("no Immich rows for an Immich source: %v", got)
	}
}
