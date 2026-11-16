//go:build !dot

package display

import "image/color"

// sunriseSkyAt is the sky's color at progress p of the light before an alarm, f of the way down the
// screen: dark red at the start and pale gold by the end, the horizon lighter than the top.
func sunriseSkyAt(p, f float64) color.RGBA {
	top := lerpRGB(color.RGBA{26, 8, 6, 255}, color.RGBA{232, 158, 78, 255}, p)
	bottom := lerpRGB(color.RGBA{74, 16, 8, 255}, color.RGBA{255, 214, 140, 255}, p)
	return lerpRGB(top, bottom, f)
}

// The time's two inks over the sky: pale while it is dim, dark once it is light.
var (
	sunrisePale = color.RGBA{255, 236, 214, 255}
	sunriseDark = color.RGBA{58, 22, 10, 255}
)

// sunriseInk is the time's color at progress p, on the sky f of the way down: whichever of the two
// inks stands off that sky more. It used to fade from one to the other while the sky brightened under
// it, and part way up the two met: the time was gray on brown, 1.4:1, at the moment a sleeper first
// looks for it. One changes to the other in a step, which nobody sees in a sunrise half an hour long.
func sunriseInk(p, f float64) color.RGBA {
	sky := sunriseSkyAt(p, f)
	if colorContrast(sunrisePale, sky) >= colorContrast(sunriseDark, sky) {
		return sunrisePale
	}
	return sunriseDark
}
