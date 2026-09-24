//go:build !dot

package display

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// testPhoto is a stand-in for a slideshow picture: "snow" is bright all over, "night" is dark all
// over, and "sky" is a bright sky over dark ground with a white cloud in it, which is light and dark
// in the same place.
func testPhoto(kind string, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			ripple := 8 * math.Sin(float64(x)/23) * math.Cos(float64(y)/17)
			var v float64
			switch kind {
			case "snow":
				v = 238 + ripple
			case "night":
				v = 22 + ripple
			default:
				v = 250 - 170*float64(y)/float64(h) + ripple
				if dx, dy := float64(x-w/3), float64(y-h/2); dx*dx/4+dy*dy < float64(h*h)/30 {
					v = 252
				}
			}
			c := uint8(min(max(v, 0), 255))
			img.SetRGBA(x, y, color.RGBA{c, c, uint8(min(float64(c)+10, 255)), 255})
		}
	}
	return img
}

// washed is a photo drawn and washed the way the slideshow does it.
func washed(photo *image.RGBA, ground color.RGBA, wash uint8) *image.RGBA {
	dst := image.NewRGBA(photo.Rect)
	draw.Draw(dst, dst.Rect, photo, image.Point{}, draw.Src)
	draw.Draw(dst, dst.Rect, image.NewUniform(color.RGBA{ground.R, ground.G, ground.B, wash}), image.Point{}, draw.Over)
	return dst
}

// A patch is as dark as the picture needs: over a bright one it brings the ground down to about
// scrimTarget, over a dark one it does nothing at all, and well away from the words it does nothing
// either way.
func TestAPatchIsAsDarkAsThePictureNeeds(t *testing.T) {
	const w, h, wash = 400, 200, 100
	ground := color.RGBA{0x1c, 0x15, 0x11, 0xff}
	for _, kind := range []string{"snow", "night"} {
		photo := testPhoto(kind, w, h)
		dst := washed(photo, ground, wash)
		before := dst.RGBAAt(200, 100)
		corner := dst.RGBAAt(5, 5)

		p := &paint{dst: dst, w: w, h: h}
		p.over.photo, p.over.ground = photo, ground
		p.over.boxes = []image.Rectangle{image.Rect(150, 80, 250, 120)}
		p.lay(p.shape(wash))

		after := dst.RGBAAt(200, 100)
		switch l := luma(after.R, after.G, after.B); {
		case kind == "night" && after != before:
			t.Errorf("a dark picture was darkened, %v to %v", before, after)
		case kind == "snow" && l > scrimTarget+3:
			t.Errorf("behind the words a bright picture is still %.0f bright", l)
		}
		if dst.RGBAAt(5, 5) != corner {
			t.Errorf("%s: a patch reached the corner", kind)
		}
	}
}

// Two patches that overlap are as dark as the darker of them, not both together.
func TestPatchesDoNotStack(t *testing.T) {
	photo := testPhoto("snow", 300, 100)
	middle := func(boxes ...image.Rectangle) color.RGBA {
		dst := image.NewRGBA(photo.Rect)
		draw.Draw(dst, dst.Rect, photo, image.Point{}, draw.Src)
		p := &paint{dst: dst, w: 300, h: 100}
		p.over.photo, p.over.ground = photo, color.RGBA{0x1c, 0x15, 0x11, 0xff}
		p.over.boxes = boxes
		p.lay(p.shape(0))
		return dst.RGBAAt(150, 50)
	}
	a := image.Rect(100, 40, 160, 60)
	b := image.Rect(140, 40, 200, 60)
	if got, want := middle(a, b), middle(a); got != want {
		t.Errorf("overlapping patches made %v, one alone %v", got, want)
	}
}

// Text on a page with no photo draws exactly as a plain font.Drawer does: nothing here touches it.
func TestTextWithoutAPhotoIsUnchanged(t *testing.T) {
	f, err := opentype.Parse(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 40, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		t.Fatal(err)
	}
	a := image.NewRGBA(image.Rect(0, 0, 300, 80))
	b := image.NewRGBA(a.Rect)
	p := &paint{dst: a, w: 300, h: 80}
	p.text(face, "2:07 PM", 10, 60, cream)
	d := &font.Drawer{Dst: b, Src: image.NewUniform(cream), Face: face, Dot: fixed.P(10, 60)}
	d.DrawString("2:07 PM")
	if string(a.Pix) != string(b.Pix) {
		t.Error("text with no photo behind it drew differently")
	}
}
