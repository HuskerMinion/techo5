//go:build !dot

package display

import "testing"

// Every figure's wires, and the glow round them, are inside its tube, clear of the socket at the foot.
func TestNixieFiguresInTube(t *testing.T) {
	for _, h := range []int{60, 270, 480} {
		w := int(float64(h)*nixieWide + 0.5)
		for f := range nixieFigures {
			for _, stroke := range figureWires(f, w, h) {
				for _, p := range stroke {
					if p.x < float64(w)*0.1 || p.x > float64(w)*0.9 || p.y < float64(h)*0.1 || p.y > float64(h)*0.85 {
						t.Errorf("tube %d tall: figure %d has a point at %.1f, %.1f", h, f, p.x, p.y)
					}
				}
			}
		}
	}
}

// A tube is drawn once for its size and figure and kept; another size, or a light ground, draws them
// again. A lit tube is not the empty one.
func TestNixieTubesKept(t *testing.T) {
	var n nixieTubes
	a := n.tube(120, 7, false)
	if n.tube(120, 7, false) != a {
		t.Error("the same tube was drawn twice")
	}
	if c := n.colonTube(120, false); c == nil || n.colonTube(120, false) != c {
		t.Error("the colon's tube was not kept")
	}
	empty := n.tube(120, -1, false)
	if string(empty.Pix) == string(a.Pix) {
		t.Error("a 7 looks like the empty tube")
	}
	if b := n.tube(140, 7, false); b == a || b.Rect.Dy() != 140 {
		t.Errorf("a new size kept the old tube: %v", b.Rect)
	}
	if dark := n.tube(140, 7, false); n.tube(140, 7, true) == dark {
		t.Error("a light ground kept the dark ground's tubes")
	}
	// Clear outside the glass: the top corners are rounded.
	if c := a.RGBAAt(0, 0); c.A != 0 {
		t.Errorf("the corner is %v, want clear", c)
	}
	if c := a.RGBAAt(a.Rect.Dx()/2, a.Rect.Dy()/2); c.A != 255 {
		t.Errorf("the middle is %v, want solid", c)
	}
}
