//go:build !dot && !spot

package display

import (
	"image"
	"image/color"
	"math"
)

// What a skin draws behind the home clock (skinLooks): Matrix's falling code, Synthwave's sunset over a
// grid, HAL 9000's eye. Each is drawn on the ground before the clock, never over a photo, and changes
// at most once a minute, so it costs one drawing a frame and no frames of its own.

// matrixGlyphs are what the code is written in. The screen's fonts have no katakana, so letters,
// figures and a few signs.
const matrixGlyphs = "0123456789ABCDEFHKMNRTXZ<>=+*:"

// matrixRain is columns of code running down behind the clock: each column a trail that is brightest at
// its head and fades behind it, and a scatter of faint characters elsewhere, all in place for the
// minute and rearranged the next.
func (r *renderer) matrixRain(s scene) {
	if s.slideshow != nil {
		return
	}
	face := r.styleFace(false, 22)
	step := r.s(26)
	rows := r.h/step + 1
	seed := uint32(s.now.Unix() / 60)
	rnd := func(a, b uint32) uint32 {
		v := a*2654435761 ^ b*40503 ^ seed*2246822519
		v ^= v >> 15
		v *= 2246822519
		v ^= v >> 13
		return v
	}
	for col := 0; col*step < r.w; col++ {
		c := uint32(col)
		head := int(rnd(c, 1) % uint32(rows+8))
		trail := 6 + int(rnd(c, 2)%14)
		for row := 0; row < rows; row++ {
			dist := head - row
			var lum float64
			switch {
			case dist == 0:
				lum = 0.38
			case dist > 0 && dist < trail:
				lum = 0.2 * (1 - float64(dist)/float64(trail))
			case rnd(c, uint32(row)+100)%11 == 0:
				lum = 0.05
			default:
				continue
			}
			ch := matrixGlyphs[rnd(c, uint32(row)+7)%uint32(len(matrixGlyphs))]
			v := uint8(255 * lum)
			r.text(face, string(ch), col*step+r.s(6), (row+1)*step, color.RGBA{v, v, v, 255})
		}
	}
}

// synthwaveSun is the neon sunset: a sun from yellow at its top to pink at the horizon, cut by bands
// in its lower half, sunk behind a grid of cyan lines running to a point on the horizon.
func (r *renderer) synthwaveSun(s scene) {
	if s.slideshow != nil {
		return
	}
	horizon := r.h * 64 / 100
	// The sun only behind a clock in the middle: beside a dial or a list it would sit under the words.
	switch clockStyle() {
	case styleClassic, styleBig, styleFlip, styleLED, styleGlow, styleNixie, styleStack, styleOverlap:
	default:
		r.synthwaveGrid(horizon)
		return
	}
	rad := float64(r.h) * 0.30
	cx, cy := float64(r.w)/2, float64(horizon)-rad*0.25
	top, bottom := rgb(0xffd319), rgb(0xff2975)
	for y := int(cy - rad); y < horizon; y++ {
		dy := float64(y) + 0.5 - cy
		if math.Abs(dy) > rad {
			continue
		}
		t := (dy + rad) / (2 * rad)
		// The bands: thicker toward the horizon.
		if t > 0.5 {
			band := int((t - 0.5) * 22)
			if math.Mod((t-0.5)*22, 1) < 0.18+0.06*float64(band) {
				continue
			}
		}
		half := math.Sqrt(rad*rad - dy*dy)
		c := lerpRGB(top, bottom, t)
		for x := int(cx - half); x <= int(cx+half); x++ {
			edge := math.Min(float64(x)-(cx-half), cx+half-float64(x))
			r.blendAt(x, y, c, 0.78*math.Min(edge, 1))
		}
	}
	r.synthwaveGrid(horizon)
}

// synthwaveGrid is the horizon, and the grid below it: rows closer together as they go away, lines
// running in to the middle of the horizon.
func (r *renderer) synthwaveGrid(horizon int) {
	cyan, pink := rgb(0x05d9e8), rgb(0xff2a6d)
	for x := 0; x < r.w; x++ {
		r.blendAt(x, horizon, pink, 0.8)
		r.blendAt(x, horizon+1, pink, 0.4)
	}
	depth := float64(r.h - horizon)
	for i := 1; i < 12; i++ {
		y := horizon + int(depth*math.Pow(float64(i)/11, 2))
		for x := 0; x < r.w; x++ {
			r.blendAt(x, y, cyan, 0.35)
		}
	}
	vx := float64(r.w) / 2
	for i := -12; i <= 12; i++ {
		foot := vx + float64(i)*float64(r.w)/9
		for y := horizon + 1; y < r.h; y++ {
			k := float64(y-horizon) / depth
			x := vx + (foot-vx)*k
			r.blendAt(int(x), y, cyan, 0.35)
		}
	}
}

// halBackdrop is the computer's eye at the top right (halEye, turn_visual.go, the HAL 9000 turn
// screen's), its glow as it is between words.
func (r *renderer) halBackdrop(s scene) {
	b := r.halEyeAt(s)
	if b.Empty() {
		return
	}
	rad := float64(b.Dx()) / 2
	halEye(r.dst, float64(b.Min.X)+rad, float64(b.Min.Y)+rad, rad, 0.55)
}

// halEyeAt is where the HAL 9000 skin's eye goes on the home clock, or empty where there is none: not
// over a photo, not while an alert's badge is there, and not behind the Overlap style, whose figures
// reach up into its corner. The Corner timer's badge goes under it (timerCorner).
func (r *renderer) halEyeAt(s scene) image.Rectangle {
	if skin() != skinHAL || s.slideshow != nil || len(s.alerts.Here) > 0 || s.style.style() == styleOverlap {
		return image.Rectangle{}
	}
	d, right := 2*r.s(44), r.w-r.margin
	return image.Rect(right-d, r.s(20), right, r.s(20)+d)
}
