//go:build !dot && !spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The camera grid's strip at the top holds its title and the time with room above them at every text
// size: it was a fixed 44 pixels, and at 125% the title touched the top of the panel.
func TestTheCameraGridsTitleHasRoomAboveIt(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { config.Use(filepath.Join(t.TempDir(), "state.json")) })
	for _, size := range []int{100, 125, 150} {
		_ = config.Set().Screen().TextSize(size)
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, skylightWide, skylightHigh)))
		m := r.small.Metrics()
		if top := r.gridTopLine() - m.Ascent.Ceil(); top < r.s(6) {
			t.Errorf("at %d%% the title's top is %d pixels from the panel's", size, top)
		}
		if foot := r.gridTop() - (r.gridTopLine() + m.Descent.Ceil()); foot < r.s(6) {
			t.Errorf("at %d%% the title's foot is %d pixels from the cells", size, foot)
		}
	}
}

// With SHOW_PREVIEW set, the camera grid of six is written there on the Show 8 and the Skylight, one
// camera with no picture and one still connecting.
func TestCameraGridPreviews(t *testing.T) {
	dir := os.Getenv("SHOW_PREVIEW")
	if dir == "" {
		t.Skip("SHOW_PREVIEW is not set")
	}
	at := time.Date(2026, 10, 20, 9, 30, 0, 0, time.Local)
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"-show8", show8Wide, show8High}, {"-skylight", skylightWide, skylightHigh}} {
		var cells []home.GridCell
		for i, name := range []string{"Front door", "Garage side", "Deck", "Dining room", "Garage inside", "Shed front"} {
			c := home.GridCell{Entity: "camera.c" + string(rune('a'+i)), Name: name}
			switch i {
			case 2:
				c.Error = "the camera did not answer"
			case 4:
			default:
				c.Frame = stalePicture(panel.wide/3, panel.high/2)
			}
			cells = append(cells, c)
		}
		s := scene{now: at, phase: "idle", showCamera: true,
			camera: home.CameraView{Entity: home.GridEntity, Until: at.Add(54 * time.Second), Grid: cells}}
		img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
		newRenderer(img).draw(s)
		f, err := os.Create(filepath.Join(dir, "camera-grid"+panel.name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
}
