//go:build !dot

package display

import (
	"fmt"
	"image"
	"image/color"
	"time"

	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// AddonScreens are the screens RenderAddonScene draws for, by the names the fake daemon takes, and their
// sizes: the Show 5's, the Show 8's and the Spot's round one.
var AddonScreens = map[string]image.Point{"show5": {960, 480}, "show8": {1280, 800}, "spot": {480, 480}}

// RenderAddonScene draws an add-on's page (addonapi.OnPage), under its name bar, or its card
// (addonapi.OnCard) at its place over the clock, on a screen of AddonScreens, with the device's own
// layout code: what the fake daemon writes as a PNG, so an author sees a scene without a device. images
// answers an image block's bytes as the device's host does; its images are decoded at once. timers
// answers the add-on's timers by id (time left, of how long), or is nil. The theme is this build's: the
// Spot's colors come from a build with the spot tag.
func RenderAddonScene(screen, name, on string, root addonapi.Block, images func(*addonapi.Block) ([]byte, string, bool),
	timers func(string) (time.Duration, time.Duration, bool)) (*image.RGBA, error) {
	size, ok := AddonScreens[screen]
	if !ok {
		return nil, fmt.Errorf("display: no screen %q; there are show5, show8 and spot", screen)
	}
	if err := addonapi.ValidScene(on, root, nil); err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
	p := &paint{dst: img, w: size.X, h: size.Y, round: screen == "spot"}
	if !p.round {
		p.sNum, p.sDen = size.X, 960 // the Show 5's sizes, scaled as the Show's renderer scales them
	}
	pal := themePalette()
	fillCanvas(img, pal.ground)
	var drawn addonDrawn
	sc := hostScene{Root: root, Rev: 1}
	if on == addonapi.OnCard {
		m := p.sceneMetrics()
		labelH := lineH(sceneFace(p.faceKey(), m.text[0], true))
		box := image.Rect(p.s(40), size.Y-size.Y/3-p.s(24), size.X-p.s(40), size.Y-p.s(24))
		if p.round {
			box = image.Rect(104, size.Y-44-160, size.X-104, size.Y-44)
		}
		inner := image.Rect(box.Min.X+m.pad, box.Min.Y+m.pad+labelH+m.gap[1], box.Max.X-m.pad, box.Max.Y-m.pad)
		v := &addonCardView{id: "addon", name: name, sc: sc, images: images, pics: picturesNow}
		var buf *image.RGBA
		p.addonCardIn(v, box, inner, &drawn, timers, false, &buf)
	} else {
		bar, closeAt, content := p.addonPageRooms()
		lay := p.laid(&drawn.page, "addon", sc, 0, content, false, images)
		p.drawScene(lay, nil, image.Point{}, pal, timers, picturesNow)
		p.addonBar(name, "", bar, closeAt, pal, 0)
	}
	if p.round {
		// The Spot's panel is a circle: what lies outside it is never seen.
		c := float64(size.X) / 2
		for y := range size.Y {
			for x := range size.X {
				if dx, dy := float64(x)+0.5-c, float64(y)+0.5-c; dx*dx+dy*dy > c*c {
					img.SetRGBA(x, y, color.RGBA{0, 0, 0, 255})
				}
			}
		}
	}
	return img, nil
}
