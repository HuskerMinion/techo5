//go:build !dot && !spot

package display

import (
	"image"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

func TestGridLayout(t *testing.T) {
	area := image.Rect(0, 0, 960, 400)
	for n, want := range map[int][2]int{1: {1, 1}, 2: {2, 1}, 3: {2, 2}, 4: {2, 2}, 5: {3, 2}, 6: {3, 2}} {
		cells := gridRects(n, area, 4)
		if len(cells) != n {
			t.Fatalf("%d cameras: %d cells", n, len(cells))
		}
		cols := want[0]
		if cells[0].Dx() > area.Dx()/cols {
			t.Errorf("%d cameras: a cell %d wide, more than %d across", n, cells[0].Dx(), cols)
		}
		for i := range cells {
			if !cells[i].In(area) {
				t.Errorf("%d cameras: cell %d %v outside %v", n, i, cells[i], area)
			}
			for j := range i {
				if cells[i].Overlaps(cells[j]) {
					t.Errorf("%d cameras: cells %d and %d overlap", n, i, j)
				}
			}
		}
	}
}

// Drawn, every cell is where a tap finds it, and the strips are not a cell.
func TestTheGridIsDrawnWhereItIsTapped(t *testing.T) {
	pic := image.NewRGBA(image.Rect(0, 0, 300, 160))
	for _, panel := range []struct {
		name       string
		wide, high int
	}{{"show 5", 960, 480}, {"show 8", 1280, 800}, {"skylight", skylightWide, skylightHigh}} {
		for _, n := range []int{2, 3, 6} {
			var cells []home.GridCell
			for i := range n {
				c := home.GridCell{Entity: "camera.x", Name: "Camera"}
				if i%2 == 0 {
					c.Frame = pic
				} else {
					c.Error = "no picture"
				}
				cells = append(cells, c)
			}
			r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
			r.draw(scene{now: time.Now(), phase: "idle", showCamera: true,
				camera: home.CameraView{Entity: home.GridEntity, Name: "Cameras", Until: time.Now().Add(time.Minute), Grid: cells}})
			for i, c := range r.gridCells(n) {
				mid := c.Min.Add(image.Pt(c.Dx()/2, c.Dy()/2))
				if got := r.gridCellAt(mid, n); got != i {
					t.Errorf("%s, %d cameras: the middle of cell %d is cell %d", panel.name, n, i, got)
				}
			}
			for _, p := range []image.Point{{r.margin, 10}, {r.margin, r.h - 5}} {
				if got := r.gridCellAt(p, n); got != -1 {
					t.Errorf("%s, %d cameras: a tap on a strip at %v is cell %d", panel.name, n, p, got)
				}
			}
			// A tap in the gap between the first two cells is one of them, not the strips' close.
			cs := r.gridCells(n)
			gap := image.Pt((cs[0].Max.X+cs[1].Min.X)/2, cs[0].Min.Y+cs[0].Dy()/2)
			if got := r.gridCellAt(gap, n); got != 0 && got != 1 {
				t.Errorf("%s, %d cameras: a tap in the gap at %v is %d", panel.name, n, gap, got)
			}
			// Every picture is drawn whole inside its cell: fitted, not cropped.
			for i, c := range cs {
				if cells[i].Frame == nil {
					continue
				}
				pic := fitCell(cells[i].Frame, c.Dx(), c.Dy(), map[fitKey]*image.RGBA{})
				if b := pic.Bounds(); b.Dx() > c.Dx() || b.Dy() > c.Dy() || (b.Dx() != c.Dx() && b.Dy() != c.Dy()) {
					t.Errorf("%s, %d cameras: cell %d is %v, its picture %v", panel.name, n, i, c.Size(), b.Size())
				}
			}
			if !r.cameraTalkAt.Empty() || !r.cameraSoundAt.Empty() {
				t.Errorf("%s: the grid has a camera's controls", panel.name)
			}
		}
	}
}
