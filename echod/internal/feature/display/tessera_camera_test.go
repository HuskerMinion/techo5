//go:build !dot && !spot

package display

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/tessera"
)

// A camera's tile draws the camera's snapshot, filling the tile, once it has come, and takes a tap
// (which puts the camera up on its own); until then it is drawn as any tile is.
func TestTesseraCameraTile(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	red := image.NewRGBA(image.Rect(0, 0, 320, 180))
	for i := 0; i < len(red.Pix); i += 4 {
		red.Pix[i], red.Pix[i+3] = 230, 255
	}
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, red, nil); err != nil {
		t.Fatal(err)
	}
	was := fetchTessCam
	asked := make(chan string, 8)
	fetchTessCam = func(_ context.Context, entity string, width int) ([]byte, error) {
		asked <- entity
		return jpg.Bytes(), nil
	}
	defer func() { fetchTessCam = was }()
	came := make(chan struct{}, 8)
	tessCams.mu.Lock()
	wasWake := tessCams.wake
	tessCams.wake = func() { came <- struct{}{} }
	tessCams.mu.Unlock()
	defer func() {
		tessCams.mu.Lock()
		tessCams.wake = wasWake
		tessCams.mu.Unlock()
	}()

	slot := func(n int) *int { return &n }
	l := &tessera.Layout{
		Rev:   "camera0123456789",
		Begin: tessera.Begin{Pages: 1, Tiles: 1, Home: 0},
		Pages: []tessera.Page{{P: 0, Title: "Home", Items: json.RawMessage(`[]`)}},
		Tiles: []tessera.Tile{
			{I: 0, Slot: slot(0), Entity: "camera.front_door", Name: "Front door", State: "idle", A: json.RawMessage(`{}`), O: json.RawMessage(`{"size":"square","display":"live"}`)},
		},
	}
	v := tessera.Place(l, 4, 3)
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, show8Wide, show8High)))
	sc := scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardTessera, tessera: v, tessOn: true}
	r.draw(sc)
	var box image.Rectangle
	zones, _ := r.dash()
	for _, z := range zones {
		if z.tess == 1 && z.entity == "camera.front_door" {
			box = z.r
		}
	}
	if box.Empty() {
		t.Fatal("the camera's tile takes no tap")
	}
	isRed := func() bool {
		c := r.dst.RGBAAt(box.Min.X+box.Dx()/2, box.Min.Y+box.Dy()/3)
		return c.R > 180 && c.G < 60 && c.B < 60
	}
	if isRed() {
		t.Fatal("a snapshot drawn before one came")
	}
	select {
	case e := <-asked:
		if e != "camera.front_door" {
			t.Fatalf("asked for %q", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no snapshot asked for")
	}
	select {
	case <-came:
	case <-time.After(5 * time.Second):
		t.Fatal("the snapshot never woke the screen")
	}
	r.draw(sc)
	if !isRed() {
		t.Fatalf("the tile is %v, not the camera's red", r.dst.RGBAAt(box.Min.X+box.Dx()/2, box.Min.Y+box.Dy()/3))
	}
	// The foot darkened for the name, more the lower down, as Tessera's own screens have it; the top of
	// the picture as it came.
	low, mid := r.dst.RGBAAt(box.Min.X+box.Dx()-r.s(20), box.Max.Y-r.s(6)), r.dst.RGBAAt(box.Min.X+box.Dx()-r.s(20), box.Min.Y+box.Dy()*7/10)
	if !(low.R < mid.R && mid.R < 220) {
		t.Errorf("the foot is not faded for the name: %v low, %v above it", low, mid)
	}
}

// fakeLive is a live picture whose every frame is one color, the size it was opened at.
type fakeLive struct {
	frame  *image.RGBA
	closed chan struct{}
}

func (f *fakeLive) Next(time.Duration) (*image.RGBA, error) {
	time.Sleep(20 * time.Millisecond)
	return f.frame, nil
}

func (f *fakeLive) Close() { close(f.closed) }

// A camera with a live picture plays it in its tile once the first snapshot is up, opened at the tile's
// size (even), and lets it go when the tile is no longer drawn.
func TestTesseraCameraTilePlaysLive(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	red := image.NewRGBA(image.Rect(0, 0, 320, 180))
	for i := 0; i < len(red.Pix); i += 4 {
		red.Pix[i], red.Pix[i+3] = 230, 255
	}
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, red, nil); err != nil {
		t.Fatal(err)
	}
	wasFetch, wasAddr, wasOpen := fetchTessCam, tessLiveAddr, openTessLive
	fetchTessCam = func(context.Context, string, int) ([]byte, error) { return jpg.Bytes(), nil }
	tessLiveAddr = func(entity string) (string, string, string, bool) {
		return "rtsp://192.0.2.30:8554/back", "", "", entity == "camera.back_door"
	}
	opened := make(chan image.Point, 1)
	live := &fakeLive{closed: make(chan struct{})}
	openTessLive = func(_ context.Context, addr, _, _ string, w, h int, _ bool) (tessLive, error) {
		if addr != "rtsp://192.0.2.30:8554/back" {
			t.Errorf("opened %q", addr)
		}
		live.frame = image.NewRGBA(image.Rect(0, 0, w, h))
		for i := 0; i < len(live.frame.Pix); i += 4 {
			live.frame.Pix[i+1], live.frame.Pix[i+3] = 200, 255
		}
		opened <- image.Pt(w, h)
		return live, nil
	}
	defer func() { fetchTessCam, tessLiveAddr, openTessLive = wasFetch, wasAddr, wasOpen }()
	tessCams.mu.Lock()
	wasWake := tessCams.wake
	tessCams.wake = func() {}
	tessCams.mu.Unlock()
	defer func() {
		tessCams.mu.Lock()
		tessCams.wake = wasWake
		tessCams.mu.Unlock()
	}()

	slot := func(n int) *int { return &n }
	l := &tessera.Layout{
		Rev:   "camera0123456789",
		Begin: tessera.Begin{Pages: 1, Tiles: 1, Home: 0},
		Pages: []tessera.Page{{P: 0, Title: "Home", Items: json.RawMessage(`[]`)}},
		Tiles: []tessera.Tile{
			{I: 0, Slot: slot(0), Entity: "camera.back_door", Name: "Back door", State: "idle", A: json.RawMessage(`{}`), O: json.RawMessage(`{"size":"square","display":"live"}`)},
		},
	}
	v := tessera.Place(l, 4, 3)
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, show8Wide, show8High)))
	sc := scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardTessera, tessera: v, tessOn: true}
	var box image.Rectangle
	r.draw(sc)
	zones, _ := r.dash()
	for _, z := range zones {
		if z.tess == 1 && z.entity == "camera.back_door" {
			box = z.r
		}
	}
	if box.Empty() {
		t.Fatal("the camera's tile takes no tap")
	}
	var size image.Point
	select {
	case size = <-opened:
	case <-time.After(10 * time.Second):
		t.Fatal("the live picture was never opened")
	}
	if size.X&1 != 0 || size.Y&1 != 0 || size.X < box.Dx() || size.Y < box.Dy() || size.X > box.Dx()+1 || size.Y > box.Dy()+1 {
		t.Fatalf("opened at %v for a tile of %v", size, box.Size())
	}
	isGreen := func() bool {
		c := r.dst.RGBAAt(box.Min.X+box.Dx()/2, box.Min.Y+box.Dy()/3)
		return c.G > 150 && c.R < 60
	}
	deadline := time.Now().Add(5 * time.Second)
	for !isGreen() {
		if time.Now().After(deadline) {
			t.Fatalf("the tile is %v, not the live picture's green", r.dst.RGBAAt(box.Min.X+box.Dx()/2, box.Min.Y+box.Dy()/3))
		}
		time.Sleep(50 * time.Millisecond)
		r.draw(sc)
	}
	// Undrawn, the picture is let go.
	tessCams.mu.Lock()
	tessCams.cams[tessCamKey("camera.back_door", false)].drawn = time.Now().Add(-time.Minute)
	tessCams.mu.Unlock()
	select {
	case <-live.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the live picture was kept after its tile went undrawn")
	}
}

// A camera's tile follows the layout: no picture without display live, the whole picture with bars for
// fit contain, and no name on it for overlay none.
func TestTesseraCameraTileFollowsTheLayout(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	red := image.NewRGBA(image.Rect(0, 0, 320, 180))
	for i := 0; i < len(red.Pix); i += 4 {
		red.Pix[i], red.Pix[i+3] = 230, 255
	}
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, red, nil); err != nil {
		t.Fatal(err)
	}
	wasFetch, wasAddr := fetchTessCam, tessLiveAddr
	asked := make(chan string, 16)
	fetchTessCam = func(_ context.Context, entity string, _ int) ([]byte, error) {
		asked <- entity
		return jpg.Bytes(), nil
	}
	tessLiveAddr = func(string) (string, string, string, bool) { return "", "", "", false }
	defer func() { fetchTessCam, tessLiveAddr = wasFetch, wasAddr }()
	tessCams.mu.Lock()
	wasWake := tessCams.wake
	tessCams.wake = func() {}
	tessCams.mu.Unlock()
	defer func() {
		tessCams.mu.Lock()
		tessCams.wake = wasWake
		tessCams.mu.Unlock()
	}()

	slot := func(n int) *int { return &n }
	tile := func(i int, entity, o string) tessera.Tile {
		return tessera.Tile{I: i, Slot: slot(2 * i), Entity: entity, Name: "Door", State: "idle", A: json.RawMessage(`{}`), O: json.RawMessage(o)}
	}
	l := &tessera.Layout{
		Rev:   "camera0123456789",
		Begin: tessera.Begin{Pages: 1, Tiles: 3, Home: 0},
		Pages: []tessera.Page{{P: 0, Title: "Home", Items: json.RawMessage(`[]`)}},
		Tiles: []tessera.Tile{
			tile(0, "camera.side_door", `{"size":"square","display":"live","fit":"contain","overlay":"none","refresh":5}`),
			tile(1, "camera.yard", `{"size":"square","display":"live","overlay":"none"}`),
			tile(2, "camera.porch", `{"size":"square"}`),
		},
	}
	v := tessera.Place(l, 4, 3)
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, show8Wide, show8High)))
	sc := scene{now: time.Now(), phase: "idle", showDash: true, dashMode: config.DashboardTessera, tessera: v, tessOn: true}
	r.draw(sc)
	boxes := map[string]image.Rectangle{}
	zones, _ := r.dash()
	for _, z := range zones {
		if z.tess != 0 {
			boxes[z.entity] = z.r
		}
	}
	got := map[string]bool{}
	for len(got) < 2 {
		select {
		case e := <-asked:
			got[e] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("snapshots asked for %v", got)
		}
	}
	time.Sleep(200 * time.Millisecond)
	r.draw(sc)
	if got["camera.porch"] {
		t.Error("a camera without display live had its snapshot fetched")
	}
	at := func(e string, fx, fy float64) color.RGBA {
		b := boxes[e]
		return r.dst.RGBAAt(b.Min.X+int(fx*float64(b.Dx())), b.Min.Y+int(fy*float64(b.Dy())))
	}
	isRed := func(c color.RGBA) bool { return c.R > 180 && c.G < 60 && c.B < 60 }
	if c := at("camera.side_door", 0.5, 0.5); !isRed(c) {
		t.Errorf("the whole picture's middle is %v", c)
	}
	if c := at("camera.side_door", 0.5, 0.1); c.R > 30 || c.G > 30 || c.B > 30 {
		t.Errorf("above the whole picture is %v, not a black bar", c)
	}
	if c := at("camera.yard", 0.5, 0.1); !isRed(c) {
		t.Errorf("the filled picture's top is %v", c)
	}
	if c := at("camera.yard", 0.5, 0.97); !isRed(c) {
		t.Errorf("the foot of a picture with no name is %v, not the picture", c)
	}
	if c := at("camera.porch", 0.5, 0.5); isRed(c) {
		t.Error("a camera without display live drew a picture")
	}
}
