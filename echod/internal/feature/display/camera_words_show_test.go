//go:build !dot && !spot

package display

import (
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// A camera with no picture says so in fixed words, whatever its error was: an error can name the
// address it failed at with a token in its query, and the screen is seen by everyone in the room. Two
// errors draw the same screen, alone and in the grid.
func TestACameraWithNoPictureSaysFixedWords(t *testing.T) {
	at := time.Date(2026, 10, 20, 9, 30, 0, 0, time.Local)
	secret := `Get "http://cam.local/snap.jpg?token=abc123": dial tcp: refused`
	for _, panel := range showPanels() {
		frame := func(v home.CameraView) *image.RGBA {
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			newRenderer(img).draw(scene{now: at, phase: "idle", showCamera: true, camera: v})
			return img
		}
		one := home.CameraView{Entity: "camera.front_door", Name: "Front door", Until: at.Add(30 * time.Second)}
		a, b := one, one
		a.Error, b.Error = "timeout", secret
		if n := changedIn(frame(a), frame(b), image.Rect(0, 0, panel.wide, panel.high)); n != 0 {
			t.Errorf("%s: the error's own words are on the screen (%d pixels differ)", panel.name, n)
		}
		grid := home.CameraView{Entity: home.GridEntity, Until: at.Add(30 * time.Second),
			Grid: []home.GridCell{{Entity: "camera.a", Name: "A"}, {Entity: "camera.b", Name: "B"}}}
		g1, g2 := grid, grid
		g1.Grid = []home.GridCell{{Entity: "camera.a", Name: "A", Error: "timeout"}, {Entity: "camera.b", Name: "B"}}
		g2.Grid = []home.GridCell{{Entity: "camera.a", Name: "A", Error: secret}, {Entity: "camera.b", Name: "B"}}
		if n := changedIn(frame(g1), frame(g2), image.Rect(0, 0, panel.wide, panel.high)); n != 0 {
			t.Errorf("%s: a grid cell's error is on the screen (%d pixels differ)", panel.name, n)
		}
	}
}
