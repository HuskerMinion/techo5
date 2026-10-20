package home

import (
	"context"
	"os"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Against a real Immich, when IMMICH_SERVER and IMMICH_KEY say which: the albums list, a page of
// photos gathers, and one of them decodes and fits the screen.
func TestImmichLive(t *testing.T) {
	srv := config.Immich{Server: os.Getenv("IMMICH_SERVER"), Key: os.Getenv("IMMICH_KEY")}
	if !srv.Set() {
		t.Skip("IMMICH_SERVER and IMMICH_KEY are not set")
	}
	ctx := context.Background()
	albums, err := ImmichAlbums(ctx, srv)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d albums", len(albums))
	for _, source := range []string{ImmichAll} {
		photos, err := immichPhotos(ctx, srv, source, immichFilter{}, 50)
		if err != nil || len(photos) == 0 {
			t.Fatalf("%s: %d photos, %v", source, len(photos), err)
		}
		b, err := immichPhoto(ctx, srv, photos[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		img, err := decodeWithin(b, maxPhotoPixels, "live", 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d photos, the first %v", source, len(photos), img.Bounds())
	}
	for _, a := range albums {
		photos, err := immichPhotos(ctx, srv, ImmichAlbumPrefix+a.ID, immichFilter{}, 1000)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("an album of %d photos: %d gathered", a.Photos, len(photos))
	}
}
