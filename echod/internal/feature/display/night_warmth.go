//go:build !dot && !spot

package display

import (
	"log/slog"
	"math"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/screen"
)

// Night warmth: through the night the screen's white goes from the panel's own (about 6500 K) to a
// lamp's, with less blue in the room: a table lookup a pixel on a copy of each frame on the cores
// (screen.Tint). Off by default.

// nightWarmKelvin is the night's white: a warm lamp's, the blue at about half and the green at
// four fifths of the panel's own, every gain at most 1 so nothing clips.
const nightWarmKelvin = 3400

// nightTint is what the screen is shown with now: the night's warmth while it is night, but not under
// the light before an alarm, whose colors are the light it gives.
func (d *Display) nightTint(now time.Time, night bool) screen.Tint {
	if !night || !config.Get().Screen.NightWarm || d.wakeLight(now) > 0 {
		return screen.None
	}
	return warmthTint(nightWarmKelvin)
}

// warmthTint is the gain on each channel that turns the panel's white (sRGB's, D65) into a black body's
// at kelvin: the black body's color in linear sRGB against that of one at 6500 K, its brightest
// channel kept at 1, and taken through the panel's gamma, since the gain applies to the values drawn.
func warmthTint(kelvin float64) screen.Tint {
	at, ref := blackBody(kelvin), blackBody(6500)
	var t screen.Tint
	top := 0.0
	for i := range t {
		t[i] = at[i] / ref[i]
		top = max(top, t[i])
	}
	for i := range t {
		t[i] = math.Pow(max(t[i]/top, 0), 1/2.2)
	}
	return t
}

// blackBody is the color of a black body at kelvin (1667 K to 25000 K) in linear sRGB, at unit
// luminance: the Planckian locus as Kim et al. fit it, from CIE xy through XYZ.
func blackBody(k float64) [3]float64 {
	k = min(max(k, 1667), 25000)
	var x float64
	if k <= 4000 {
		x = -0.2661239e9/(k*k*k) - 0.2343589e6/(k*k) + 0.8776956e3/k + 0.179910
	} else {
		x = -3.0258469e9/(k*k*k) + 2.1070379e6/(k*k) + 0.2226347e3/k + 0.240390
	}
	var y float64
	switch {
	case k <= 2222:
		y = -1.1063814*x*x*x - 1.34811020*x*x + 2.18555832*x - 0.20219683
	case k <= 4000:
		y = -0.9549476*x*x*x - 1.37418593*x*x + 2.09137015*x - 0.16748867
	default:
		y = 3.0817580*x*x*x - 5.87338670*x*x + 3.75112997*x - 0.37001483
	}
	X, Y, Z := x/y, 1.0, (1-x-y)/y
	return [3]float64{
		3.2406*X - 1.5372*Y - 0.4986*Z,
		-0.9689*X + 1.8758*Y + 0.0415*Z,
		0.0557*X - 0.2040*Y + 1.0570*Z,
	}
}

func nightWarmthSwitch(d *Display) *esphome.Switch {
	s := &esphome.Switch{
		Base: esphome.Base{
			ObjectID: "screen_night_warmth",
			Name:     "Night warmth",
			Icon:     "mdi:weather-night",
			Category: esphome.CategoryConfig,
		},
	}
	s.OnCommand = d.setNightWarm
	return s
}

// setNightWarm saves the setting, tells Home Assistant and shows it at once, so it can be judged
// while looking.
func (d *Display) setNightWarm(on bool) {
	if err := config.Set().Screen().NightWarm(on); err != nil {
		slog.Error("saving a setting failed", "setting", "screen_night_warmth", "err", err)
		on = config.Get().Screen.NightWarm
	} else {
		slog.Info("setting changed", "setting", "screen_night_warmth", "using", on)
	}
	if d.warmthSw != nil {
		d.warmthSw.Set(on)
	}
	d.wake()
}
