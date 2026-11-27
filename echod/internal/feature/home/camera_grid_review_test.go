package home

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A camera slow to answer holds up nobody else's picture.
func TestASlowCameraHoldsUpNoOther(t *testing.T) {
	f, _ := gridHouse(t, []config.Camera{{Entity: "camera.slow", Name: "Slow"}, {Entity: "camera.quick", Name: "Quick"}})
	release := make(chan struct{})
	defer close(release)
	quick := fetchCamera
	fetchCamera = func(ctx context.Context, entity string, w int) ([]byte, error) {
		if entity == "camera.slow" {
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return quick(ctx, entity, w)
	}
	if err := f.ShowGrid(nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v, _ := f.Camera(); len(v.Grid) == 2 && v.Grid[1].Frame != nil {
			if v.Grid[0].Frame != nil {
				t.Error("the slow camera had a picture before it answered")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("the quick camera waited on the slow one")
}

// Only cameras go in the grid, and put up on the device it stays as long as asked, "Until tapped" too
// (#748).
func TestTheGridTakesCamerasForAsLongAsAsked(t *testing.T) {
	f, _ := gridHouse(t, []config.Camera{{Entity: "camera.a", Name: "A"}})
	if err := f.ShowGrid([]string{"light.kitchen", "grid", "local"}, time.Minute); err == nil {
		t.Error("a grid of things that are not cameras went up")
	}
	if err := f.ShowGrid([]string{"camera.a"}, 48*time.Hour); err != nil {
		t.Fatal(err)
	}
	v, _ := f.Camera()
	if left := time.Until(v.Until); left < 47*time.Hour {
		t.Errorf("put up on the device for 48h, up for %v", left)
	}

}

// The grid's own name asked for as a camera is not a camera: the grid stays as it is.
func TestTheGridsNameIsNotACamera(t *testing.T) {
	f, _ := gridHouse(t, []config.Camera{{Entity: "camera.a", Name: "A"}, {Entity: "camera.b", Name: "B"}})
	if err := f.ShowGrid(nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	f.ShowCamera(GridEntity, time.Minute)
	if v, _ := f.Camera(); v.Entity != GridEntity || len(v.Grid) != 2 {
		t.Errorf("after asking for %q as a camera: %q with %d cells", GridEntity, v.Entity, len(v.Grid))
	}
}

// The snapshot is asked for at the cell's width, and a grid taken down gives up on the fetches it was
// waiting on.
func TestTheGridAsksForSmallPicturesAndLetsGo(t *testing.T) {
	f, _ := gridHouse(t, []config.Camera{{Entity: "camera.a", Name: "A"}, {Entity: "camera.b", Name: "B"}})
	widths := make(chan int, 8)
	given := make(chan struct{}, 8)
	fetchCamera = func(ctx context.Context, _ string, w int) ([]byte, error) {
		widths <- w
		<-ctx.Done()
		given <- struct{}{}
		return nil, ctx.Err()
	}
	if err := f.ShowGrid(nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	if w := <-widths; w <= 0 || w > cameraFrameW/2 {
		t.Errorf("asked for %d wide, want at most a cell's %d", w, cameraFrameW/2)
	}
	f.HideCamera()
	select {
	case <-given:
	case <-time.After(2 * time.Second):
		t.Error("a fetch went on after the grid was taken down")
	}
}

// A go2rtc stream in the grid is fetched from go2rtc, never through Home Assistant's camera proxy.
func TestAGo2RTCCameraInTheGridAsksGo2RTC(t *testing.T) {
	f, _ := gridHouse(t, []config.Camera{{Entity: "go2rtc:porch", Name: "Porch"}, {Entity: "camera.quick", Name: "Quick"}})
	picture := fetchCamera
	var mu sync.Mutex
	viaHA, viaGo2RTC := map[string]bool{}, map[string]bool{}
	fetchCamera = func(ctx context.Context, entity string, w int) ([]byte, error) {
		mu.Lock()
		viaHA[entity] = true
		mu.Unlock()
		return picture(ctx, "camera.quick", w)
	}
	was := gridGo2RTC
	gridGo2RTC = func(ctx context.Context, entity string, w int) ([]byte, error) {
		mu.Lock()
		viaGo2RTC[entity] = true
		mu.Unlock()
		return picture(ctx, "camera.quick", w)
	}
	t.Cleanup(func() { gridGo2RTC = was })
	if err := f.ShowGrid(nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := viaGo2RTC["go2rtc:porch"] && viaHA["camera.quick"]
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if !viaGo2RTC["go2rtc:porch"] || viaHA["go2rtc:porch"] {
		t.Errorf("go2rtc %v, Home Assistant %v: the go2rtc stream should come from go2rtc alone", viaGo2RTC, viaHA)
	}
}
