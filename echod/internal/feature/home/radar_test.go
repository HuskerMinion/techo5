package home

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestWorldPixel(t *testing.T) {
	x, y := worldPixel(0, 0, 0)
	if math.Abs(x-128) > 1e-9 || math.Abs(y-128) > 1e-9 {
		t.Errorf("0,0 at zoom 0 = %v,%v, want 128,128", x, y)
	}
	x, y = worldPixel(51.5, -0.12, 8) // London is in tile 127, 85 at zoom 8
	if int(x)/tileSize != 127 || int(y)/tileSize != 85 {
		t.Errorf("London at zoom 8 is in tile %d,%d", int(x)/tileSize, int(y)/tileSize)
	}
}

func TestFloorDiv(t *testing.T) {
	for _, c := range [][3]int{{0, 256, 0}, {255, 256, 0}, {256, 256, 1}, {-1, 256, -1}, {-256, 256, -1}, {-257, 256, -2}} {
		if got := floorDiv(c[0], c[1]); got != c[2] {
			t.Errorf("floorDiv(%d, %d) = %d, want %d", c[0], c[1], got, c[2])
		}
	}
}

// A mosaic puts each tile where its world pixels fall, wrapping across the date line.
func TestMosaic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /x/y: a tile whose red is x and green y.
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		x, _ := strconv.Atoi(parts[0])
		y, _ := strconv.Atoi(parts[1])
		img := image.NewRGBA(image.Rect(0, 0, tileSize, tileSize))
		for i := range img.Pix {
			switch i % 4 {
			case 0:
				img.Pix[i] = uint8(x)
			case 1:
				img.Pix[i] = uint8(y)
			case 3:
				img.Pix[i] = 255
			}
		}
		var b bytes.Buffer
		_ = png.Encode(&b, img)
		_, _ = w.Write(b.Bytes())
	}))
	defer srv.Close()

	// Zoom 2 is 4 by 4 tiles. Start 100 px left of the world's edge, 10 px into row 1.
	img, err := mosaic(context.Background(), 300, 100, -100, tileSize+10, 2, 4, func(x, y int) string {
		return srv.URL + "/" + strconv.Itoa(x) + "/" + strconv.Itoa(y)
	})
	if err != nil {
		t.Fatal(err)
	}
	check := func(px, py int, want color.RGBA) {
		t.Helper()
		if got := img.RGBAAt(px, py); got != want {
			t.Errorf("pixel %d,%d = %v, want %v", px, py, got, want)
		}
	}
	check(0, 0, color.RGBA{3, 1, 0, 255})    // wrapped from tile 3
	check(99, 99, color.RGBA{3, 1, 0, 255})  // still tile 3
	check(100, 0, color.RGBA{0, 1, 0, 255})  // tile 0
	check(299, 99, color.RGBA{0, 1, 0, 255}) // tile 0 to the right edge
}

// The map comes back from disk after a restart, as it was, only for the place it shows, and a map
// kept for a new place takes the old one's place rather than piling up beside it.
func TestTheMapIsKeptAcrossRestarts(t *testing.T) {
	defer func(was string) { mapDir = was }(mapDir)
	mapDir = t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, radarW, radarH))
	img.Pix[0], img.Pix[3] = 200, 255
	saveMap(41.25, -96.0, img)
	got := savedMap(41.25, -96.0)
	if got == nil || got.Pix[0] != 200 {
		t.Fatal("the kept map did not come back")
	}
	if savedMap(40.0, -96.0) != nil {
		t.Error("a map kept for one place came back for another")
	}
	saveMap(40.0, -96.0, img)
	if kept, _ := filepath.Glob(filepath.Join(mapDir, "radar-map-*.png")); len(kept) != 1 {
		t.Errorf("%d maps kept, want the one", len(kept))
	}
}
