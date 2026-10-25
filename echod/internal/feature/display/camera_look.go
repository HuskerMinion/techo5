//go:build !dot

package display

import (
	"image"
	"image/color"
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// How the camera view looks: Camera picture, how the camera's picture takes the screen (whole and as
// large as the screen takes it, filling it with the rest cut off, or at the size the camera sends it),
// Camera strips, the strips under its name and clock and under "tap to close" (a solid shade, or a
// shade that fades into the picture, the words in both centered alike at the top and the foot), and
// Camera strips shade, how dark they are. All on the screen's settings (Display, Pop-ups) and in Home
// Assistant, beside Camera time; the Show's camera view draws them (render_camera.go, feature/home's
// cameraFrame).

type cameraLook struct {
	label string
	value string // what the config keeps; "" is the default
}

var cameraFits = []cameraLook{
	{"Whole picture", ""},
	{"Fill the screen", "fill"},
	{"Camera's own size", "native"},
}

var cameraBandStyles = []cameraLook{
	{"Solid", ""},
	{"Fade", "fade"},
}

var cameraShades = []cameraLook{
	{"Light", "light"},
	{"Medium", ""},
	{"Strong", "strong"},
}

func cameraLookLabels(looks []cameraLook) []string {
	out := make([]string, len(looks))
	for i, l := range looks {
		out[i] = l.label
	}
	return out
}

// cameraLookIndex is v's place in looks; a value no choice has reads as the default.
func cameraLookIndex(looks []cameraLook, v string) int {
	for i, l := range looks {
		if l.value == v {
			return i
		}
	}
	return 0
}

func cameraFitIndex() int { return cameraLookIndex(cameraFits, config.Get().Screen.CameraFit) }
func cameraBandsIndex() int {
	return cameraLookIndex(cameraBandStyles, config.Get().Screen.CameraBands)
}
func cameraShadeIndex() int { return cameraLookIndex(cameraShades, config.Get().Screen.CameraShade) }

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

// The Home Assistant settings, made by the display where the screen has a camera view (display.go).
var cameraFitSel, cameraBandsSel, cameraShadeSel *esphome.Select

// setCameraFit saves the choice at i and shows it in Home Assistant.
func setCameraFit(i int) {
	if i < 0 || i >= len(cameraFits) {
		return
	}
	if err := config.Set().Screen().CameraFit(cameraFits[i].value); err != nil {
		slog.Error("saving the camera picture failed", "err", err)
		return
	}
	if cameraFitSel != nil {
		cameraFitSel.Set(cameraFits[i].label)
	}
}

// setCameraBands saves the choice at i and shows it in Home Assistant.
func setCameraBands(i int) {
	if i < 0 || i >= len(cameraBandStyles) {
		return
	}
	if err := config.Set().Screen().CameraBands(cameraBandStyles[i].value); err != nil {
		slog.Error("saving the camera strips failed", "err", err)
		return
	}
	if cameraBandsSel != nil {
		cameraBandsSel.Set(cameraBandStyles[i].label)
	}
}

// setCameraShade saves the choice at i and shows it in Home Assistant.
func setCameraShade(i int) {
	if i < 0 || i >= len(cameraShades) {
		return
	}
	if err := config.Set().Screen().CameraShade(cameraShades[i].value); err != nil {
		slog.Error("saving the camera strips' shade failed", "err", err)
		return
	}
	if cameraShadeSel != nil {
		cameraShadeSel.Set(cameraShades[i].label)
	}
}

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
