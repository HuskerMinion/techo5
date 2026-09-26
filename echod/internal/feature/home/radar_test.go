package home

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
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
	saveMap(10.0, 20.0, img)
	got := savedMap(10.0, 20.0)
	if got == nil || got.Pix[0] != 200 {
		t.Fatal("the kept map did not come back")
	}
	if savedMap(30.0, 20.0) != nil {
		t.Error("a map kept for one place came back for another")
	}
	saveMap(30.0, 20.0, img)
	if kept, _ := filepath.Glob(filepath.Join(mapDir, "radar-map-*.png")); len(kept) != 1 {
		t.Errorf("%d maps kept, want the one", len(kept))
	}
}

// fakeRadar serves a RainViewer index of n past frames and a tile for anything else, counting what is
// asked for by frame, and failing every tile of the frame named in fail.
type fakeRadar struct {
	mu    sync.Mutex
	n     int
	fail  string
	asked map[string]int
}

func (fr *fakeRadar) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/index.json" {
		var past []string
		for i := range fr.n {
			past = append(past, fmt.Sprintf(`{"time":%d,"path":"/frame%d"}`, 1_700_000_000+600*i, i))
		}
		fmt.Fprintf(w, `{"host":"http://%s","radar":{"past":[%s]}}`, r.Host, strings.Join(past, ","))
		return
	}
	frame := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)[0]
	fr.mu.Lock()
	fr.asked[frame]++
	fail := frame == fr.fail
	fr.mu.Unlock()
	if fail {
		http.Error(w, "no", http.StatusInternalServerError)
		return
	}
	img := image.NewNRGBA(image.Rect(0, 0, tileSize, tileSize))
	_ = png.Encode(w, img)
}

func (fr *fakeRadar) count(frame string) int {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	return fr.asked[frame]
}

// The radar builds every frame in order; a second build asks for nothing it already has; and a frame
// that fails keeps the others that arrived, so the retry asks only for the one that was missing.
func TestTheRadarBuildsAndReusesItsFrames(t *testing.T) {
	defer func(i, m, d string) { radarIndex, baseTiles, mapDir = i, m, d }(radarIndex, baseTiles, mapDir)
	fr := &fakeRadar{n: 4, asked: map[string]int{}}
	srv := httptest.NewServer(fr)
	defer srv.Close()
	radarIndex, baseTiles, mapDir = srv.URL+"/index.json", srv.URL+"/map/%d/%d/%d.png", t.TempDir()
	config.Use(filepath.Join(t.TempDir(), "state.json")) // automatic: RainViewer, since 10, 20 is not the U.S.

	f := &Feature{}
	if err := f.buildRadarAt(10, 20); err != nil {
		t.Fatal(err)
	}
	v := f.radar.view
	if len(v.Frames) != 4 || !v.Frames[0].At.Before(v.Frames[3].At) {
		t.Fatalf("%d frames, want 4 oldest first", len(v.Frames))
	}
	first := fr.count("frame0")
	if err := f.buildRadarAt(10, 20); err != nil || fr.count("frame0") != first {
		t.Errorf("a second build fetched a frame it had (err %v)", err)
	}

	// A new index: one frame gone, two new, and one of the new ones failing.
	fr.mu.Lock()
	fr.n, fr.fail = 6, "frame4"
	fr.mu.Unlock()
	if err := f.buildRadarAt(10, 20); err == nil {
		t.Fatal("a frame that failed did not fail the build")
	}
	if _, ok := f.radar.made["rv/frame5"]; !ok {
		t.Error("the newest frame that arrived was not kept for the retry")
	}
	fr.mu.Lock()
	fr.fail = ""
	fr.mu.Unlock()
	five := fr.count("frame5")
	if err := f.buildRadarAt(10, 20); err != nil {
		t.Fatal(err)
	}
	if fr.count("frame5") != five || len(f.radar.view.Frames) != radarFrames {
		t.Errorf("the retry fetched a frame it had, or has %d frames", len(f.radar.view.Frames))
	}
}
