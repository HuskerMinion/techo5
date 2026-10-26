//go:build !dot

package display

import (
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
