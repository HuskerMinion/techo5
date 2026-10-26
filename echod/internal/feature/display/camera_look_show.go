//go:build !dot && !spot

package display

import (
	"image"
	"image/color"

	esphome "github.com/ygelfand/go-esphome-device"
)

// What only the Show's camera view draws and its display offers Home Assistant of the camera look
// (camera_look.go): the Spot's round view has no strips (hasCameraLook).

// cameraStripShade is the black the strips are shaded with: the usual one (the page's shade, or the
// deeper one a fading strip starts from at the edge), lighter or stronger as Camera strips shade says.
func cameraStripShade(fade bool) color.RGBA {
	alpha := [3]uint8{0x60, 0x90, 0xc8} // solid: Light, Medium, Strong
	if fade {
		alpha = [3]uint8{0x80, 0xb0, 0xe0}
	}
	return color.RGBA{0, 0, 0, alpha[cameraShadeIndex()]}
}

// cameraBandsFade is whether the camera view's strips fade into the picture.
func cameraBandsFade() bool { return cameraBandStyles[cameraBandsIndex()].value == "fade" }

// cameraLookSelect is a Home Assistant setting of looks, saved by set.
func cameraLookSelect(id, name, icon string, looks []cameraLook, set func(int)) *esphome.Select {
	s := &esphome.Select{
		Base:    esphome.Base{ObjectID: id, Name: name, Icon: icon, Category: esphome.CategoryConfig},
		Options: cameraLookLabels(looks),
	}
	s.OnCommand = func(v string) {
		for i, l := range looks {
			if l.label == v {
				set(i)
				return
			}
		}
	}
	return s
}

func cameraFitSelect() *esphome.Select {
	return cameraLookSelect("screen_camera_picture", "Camera picture", "mdi:fit-to-screen-outline", cameraFits, setCameraFit)
}

func cameraBandsSelect() *esphome.Select {
	return cameraLookSelect("screen_camera_strips", "Camera strips", "mdi:gradient-vertical", cameraBandStyles, setCameraBands)
}

func cameraShadeSelect() *esphome.Select {
	return cameraLookSelect("screen_camera_strips_shade", "Camera strips shade", "mdi:brightness-6", cameraShades, setCameraShade)
}

// fadeStrip darkens r with c's shade, all of it at the edge of the screen (the top for top, else the
// foot) and none of it at the other edge, so the strip fades into the picture.
func fadeStrip(dst *image.RGBA, r image.Rectangle, c color.RGBA, top bool) {
	h := r.Dy()
	for i := range h {
		k := i + 1 // rows from the inner edge: none of the shade at the first
		if top {
			k = h - i
		}
		a := uint8(int(c.A) * k / h)
		y := r.Min.Y + i
		// c is premultiplied: its color scales with the alpha.
		f := func(v uint8) uint8 { return uint8(int(v) * int(a) / max(int(c.A), 1)) }
		washOver(dst, image.Rect(r.Min.X, y, r.Max.X, y+1), color.RGBA{f(c.R), f(c.G), f(c.B), a})
	}
}
