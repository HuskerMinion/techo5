//go:build !dot

package display

import (
	"image"
	"testing"

	"golang.org/x/image/font/basicfont"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// Two towns in one spot get one name, the larger's; a town whose name would cross a kept-out area is
// named on its other side, or not at all.
func TestTownNamesDoNotCollide(t *testing.T) {
	dst := image.NewRGBA(image.Rect(0, 0, 400, 200))
	all := func(image.Rectangle) bool { return true }
	count := func() int {
		n := 0
		for i := 0; i < len(dst.Pix); i += 4 {
			if dst.Pix[i] == placeInk.R && dst.Pix[i+1] == placeInk.G {
				n++
			}
		}
		return n
	}
	one := []home.RadarPlace{{Name: "Bigtown", At: image.Pt(100, 100), Pop: 900}}
	drawPlaces(dst, basicfont.Face7x13, one, nil, all, 10)
	alone := count()

	dst = image.NewRGBA(image.Rect(0, 0, 400, 200))
	two := append(one, home.RadarPlace{Name: "Smallton", At: image.Pt(104, 101), Pop: 100})
	drawPlaces(dst, basicfont.Face7x13, two, nil, all, 10)
	if count() > alone+30 { // the second town's dot at most, not its name
		t.Error("a town on top of another was named too")
	}

	dst = image.NewRGBA(image.Rect(0, 0, 400, 200))
	right := []image.Rectangle{image.Rect(110, 0, 400, 200)} // everything right of the town kept out
	drawPlaces(dst, basicfont.Face7x13, one, right, all, 10)
	if count() == 0 {
		t.Error("the name was not moved to the left of its dot")
	}
	for y := 0; y < 200; y++ {
		for x := 111; x < 400; x++ {
			if p := dst.RGBAAt(x, y); p.A != 0 {
				t.Fatalf("drew at %d,%d inside the kept-out area", x, y)
			}
		}
	}
}
