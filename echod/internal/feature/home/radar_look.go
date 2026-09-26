package home

import (
	"context"
	"fmt"
	"image"
	"image/draw"
	"math"

	xdraw "golang.org/x/image/draw"
)

// The rain map's look, the same whichever source the radar comes from: a satellite-style map of the
// ground (NASA's Blue Marble), the clouds seen from above as a soft gray veil (a GOES weather
// satellite's infrared, where one covers home), and the rain in soft greens through yellow, orange and
// red. Neither source lets a free user choose its colors, so each tile's colors are turned back into
// reflectivity (dBZ) with the source's own published table, smoothed, and painted again here.

// NASA GIBS: public domain, no key. Blue Marble stops at zoom 8, the map's zoom; the infrared at 6.
var (
	baseTiles  = "https://gibs.earthdata.nasa.gov/wmts/epsg3857/best/BlueMarble_ShadedRelief_Bathymetry/default/GoogleMapsCompatible_Level8/%d/%d/%d.jpeg"
	cloudTiles = "https://gibs.earthdata.nasa.gov/wmts/epsg3857/best/%s/default/GoogleMapsCompatible_Level6/%d/%d/%d.png"
)

const cloudZoom = 6

// paletteColor is one entry of a source's color table: the dBZ a color stands for.
type paletteColor struct {
	dBZ     float32
	r, g, b uint8
}

// noRain marks a pixel with no echo.
const noRain = -99

// reflectivity turns a radar picture back into dBZ, pixel by pixel, by the nearest color of the
// source's table. Only solid pixels count: the edges a tile server blended into the ground match the
// wrong colors (a pale green edge reads as a red core), so they are left out, and so is any color far
// from every one in the table.
func reflectivity(img *image.RGBA, table []paletteColor) []float32 {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	out := make([]float32, w*h)
	known := map[uint32]float32{} // a tile has a few dozen colors; each is looked up once
	for y := 0; y < h; y++ {
		row := img.Pix[y*img.Stride:]
		for x := 0; x < w; x++ {
			p := row[x*4 : x*4+4]
			if p[3] < 250 {
				out[y*w+x] = noRain
				continue
			}
			key := uint32(p[0])<<16 | uint32(p[1])<<8 | uint32(p[2])
			v, ok := known[key]
			if !ok {
				v = nearestDBZ(p[0], p[1], p[2], table)
				known[key] = v
			}
			out[y*w+x] = v
		}
	}
	return out
}

func nearestDBZ(r, g, b uint8, table []paletteColor) float32 {
	best, bestD := float32(noRain), 40*40+1 // farther than 40 in RGB is not this table's color
	for _, c := range table {
		dr, dg, db := int(r)-int(c.r), int(g)-int(c.g), int(b)-int(c.b)
		if d := dr*dr + dg*dg + db*db; d < bestD {
			best, bestD = c.dBZ, d
		}
	}
	return best
}

// rainStop is a point of the ramp: a dBZ and its color with how solid it is.
type rainStop struct {
	dBZ        float32
	r, g, b, a float32
}

// rainRamp is the look: soft greens for light rain, yellow, orange, red, and magenta for hail cores,
// clearer at the light end so the ground shows through a drizzle.
var rainRamp = []rainStop{
	{10, 95, 190, 140, 0},
	{18, 110, 205, 145, 120},
	{25, 60, 175, 95, 175},
	{32, 25, 125, 55, 195},
	{38, 240, 225, 60, 215},
	{44, 245, 150, 40, 225},
	{50, 225, 45, 35, 235},
	{58, 180, 20, 90, 240},
	{65, 230, 120, 240, 245},
}

// paintRain smooths a w by h reflectivity field and paints it through the ramp into a picture of
// dst's size, the field stretched to fit. Radar is blocky close up; the smoothing (a blur that only
// counts pixels with an echo, so rain does not bleed dark into the ground) gives the soft edges.
//
// The blur is done on the field at its own size, before it is stretched: a quarter of the work for a
// RainViewer field, and the same look, since stretching a blurred field smoothly is itself a blur. It
// is then read at each of dst's pixels between its four nearest points.
func paintRain(dst *image.RGBA, field []float32, w, h int) {
	W, H := dst.Bounds().Dx(), dst.Bounds().Dy()
	val := make([]float32, w*h)
	has := make([]float32, w*h)
	for i, v := range field {
		if v > noRain+1 {
			val[i], has[i] = v, 1
		}
	}
	// 2.2 pixels at the picture's size, whatever the field's.
	sigma := max(2.2*float64(w)/float64(W), 0.6)
	blur(val, w, h, sigma)
	blur(has, w, h, sigma)
	sx, sy := float32(w)/float32(W), float32(h)/float32(H)
	for y := 0; y < H; y++ {
		fy := max((float32(y)+0.5)*sy-0.5, 0)
		y0 := min(int(fy), h-1)
		y1, ty := min(y0+1, h-1), fy-float32(y0)
		row := dst.Pix[y*dst.Stride:]
		for x := 0; x < W; x++ {
			fx := max((float32(x)+0.5)*sx-0.5, 0)
			x0 := min(int(fx), w-1)
			x1, tx := min(x0+1, w-1), fx-float32(x0)
			lerp := func(f []float32) float32 {
				top := f[y0*w+x0]*(1-tx) + f[y0*w+x1]*tx
				bot := f[y1*w+x0]*(1-tx) + f[y1*w+x1]*tx
				return top*(1-ty) + bot*ty
			}
			hv := lerp(has)
			if hv < 0.25 {
				continue
			}
			c := rampAt(lerp(val) / hv)
			a := c.a * min(1, hv*1.4) / 255
			if a <= 0 {
				continue
			}
			p := row[x*4 : x*4+3 : x*4+3]
			p[0] = uint8(float32(p[0])*(1-a) + c.r*a)
			p[1] = uint8(float32(p[1])*(1-a) + c.g*a)
			p[2] = uint8(float32(p[2])*(1-a) + c.b*a)
		}
	}
}

func rampAt(v float32) rainStop {
	if v <= rainRamp[0].dBZ {
		return rainStop{a: 0}
	}
	last := rainRamp[len(rainRamp)-1]
	if v >= last.dBZ {
		return last
	}
	for i := 1; i < len(rainRamp); i++ {
		hi := rainRamp[i]
		if v <= hi.dBZ {
			lo := rainRamp[i-1]
			t := (v - lo.dBZ) / (hi.dBZ - lo.dBZ)
			return rainStop{v, lo.r + (hi.r-lo.r)*t, lo.g + (hi.g-lo.g)*t, lo.b + (hi.b-lo.b)*t, lo.a + (hi.a-lo.a)*t}
		}
	}
	return last
}

// blur is a Gaussian blur of a w by h field, in place: a row pass and a column pass.
func blur(f []float32, w, h int, sigma float64) {
	r := int(math.Ceil(sigma * 3))
	k := make([]float32, 2*r+1)
	var sum float32
	for i := range k {
		d := float64(i - r)
		k[i] = float32(math.Exp(-d * d / (2 * sigma * sigma)))
		sum += k[i]
	}
	for i := range k {
		k[i] /= sum
	}
	tmp := make([]float32, max(w, h))
	for y := 0; y < h; y++ {
		row := f[y*w : y*w+w]
		for x := 0; x < w; x++ {
			var s float32
			for j, kv := range k {
				s += kv * row[clampInt(x+j-r, 0, w-1)]
			}
			tmp[x] = s
		}
		copy(row, tmp[:w])
	}
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			var s float32
			for j, kv := range k {
				s += kv * f[clampInt(y+j-r, 0, h-1)*w+x]
			}
			tmp[y] = s
		}
		for y := 0; y < h; y++ {
			f[y*w+x] = tmp[y]
		}
	}
}

func clampInt(v, lo, hi int) int {
	return min(max(v, lo), hi)
}

// tintBase darkens Blue Marble toward the screen, a little greener than it comes.
func tintBase(img *image.RGBA) {
	p := img.Pix
	for i := 0; i+3 < len(p); i += 4 {
		p[i] = uint8(float32(p[i]) * 0.62)
		p[i+1] = uint8(float32(p[i+1]) * 0.72)
		p[i+2] = uint8(float32(p[i+2]) * 0.66)
		p[i+3] = 255
	}
}

// cloudLayer is the GOES satellite whose infrared covers a longitude, or "" where neither does.
// GOES-East sits over 75°W and GOES-West over 137°W; each sees well about 60° either side.
func cloudLayer(lon float64) string {
	switch {
	case lon >= -106 && lon <= -15:
		return "GOES-East_ABI_Band13_Clean_Infrared"
	case lon >= -180 && lon < -106 || lon > 165:
		return "GOES-West_ABI_Band13_Clean_Infrared"
	}
	return ""
}

// clouds lays the clouds over the map: cold (bright) infrared cloud tops as a soft gray veil, faint
// for thin cloud and heavier for deep. x0, y0 is the picture's top left at the map's zoom.
func clouds(ctx context.Context, dst *image.RGBA, lon float64, x0, y0 int) error {
	layer := cloudLayer(lon)
	if layer == "" {
		return nil
	}
	W, H := dst.Bounds().Dx(), dst.Bounds().Dy()
	shift := mapZoom - cloudZoom
	scale := 1 << shift
	ir, err := mosaic(ctx, W/scale+1, H/scale+1, floorDiv(x0, scale), floorDiv(y0, scale), cloudZoom, 4, func(x, y int) string {
		return fmt.Sprintf(cloudTiles, layer, cloudZoom, y, x)
	})
	if err != nil {
		return fmt.Errorf("clouds: %w", err)
	}
	big := image.NewRGBA(image.Rect(0, 0, W, H))
	// A quarter-size picture stretched four times over: cloud tops are soft anyway.
	xdraw.ApproxBiLinear.Scale(big, big.Bounds(), ir, image.Rect(0, 0, W/scale, H/scale), draw.Src, nil)
	p, q := dst.Pix, big.Pix
	for i := 0; i+3 < len(p); i += 4 {
		lum := (299*int(q[i]) + 587*int(q[i+1]) + 114*int(q[i+2])) / 1000
		a := veil[lum] * float32(q[i+3]) / 255
		if a <= 0 {
			continue
		}
		for c := 0; c < 3; c++ {
			p[i+c] = uint8(float32(p[i+c])*(1-a) + 175*a)
		}
	}
	return nil
}

// veil is how thick the cloud veil is for each brightness of the infrared: nothing for warm ground
// and low cloud, rising for colder, higher tops. Worked out once, not for every pixel.
var veil = func() (t [256]float32) {
	for lum := range t {
		if x := (float64(lum)/255 - 0.42) / 0.45; x > 0 {
			t[lum] = float32(math.Pow(math.Min(x, 1), 1.2) * 0.62)
		}
	}
	return t
}()
