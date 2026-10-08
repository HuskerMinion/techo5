//go:build !dot

package display

import (
	"hash/fnv"
	"image"
	"image/color"
	"image/draw"
	"log/slog"
	"math"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/vector"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// Weather art: the slideshow's other picture, a landscape the device draws for the weather and the
// time of day (config Slideshow.Art). Sky, sun or moon and stars, three ranges of mountains, pines on
// a hill, clouds, snow on the peaks, fog in the valleys: all shapes and gradients drawn here, so there
// are no pictures to ship and nothing to fetch. Each device's mountains come from its own name, so no
// two look alike.
//
// The landscape is drawn once for each weather and time of day, off the drawing goroutine. The clouds
// are drawn once as soft shapes and drift across it, a step a second; rain, snow, storms and fog move
// over it at the weather page's pace (weatherfx.go), while Weather animation is on.

// artCond is the weather as the art draws it.
type artCond int

const (
	artClear artCond = iota
	artPartly
	artCloudy
	artRain
	artStorm
	artSnow
	artFog
)

// artCondFor is Home Assistant's condition as a sky.
func artCondFor(cond string) artCond {
	switch strings.ToLower(cond) {
	case "partlycloudy", home.PartlyCloudyNight, "windy", "windy-variant":
		return artPartly
	case "cloudy", "exceptional":
		return artCloudy
	case "rainy", "pouring", "hail":
		return artRain
	case "lightning", "lightning-rainy":
		return artStorm
	case "snowy", "snowy-rainy":
		return artSnow
	case "fog":
		return artFog
	}
	return artClear
}

// artTime is the part of the day, which sets the sky's colors and the light.
type artTime int

const (
	artDawn artTime = iota
	artDay
	artDusk
	artNight
)

// artTimeFor is the part of the day at now: from the day's sunrise and sunset when they are known, from
// the clock when not.
func artTimeFor(now, rise, set time.Time, ok bool) artTime {
	if !ok {
		h := float64(now.Hour()) + float64(now.Minute())/60
		switch {
		case h >= 6 && h < 7.5:
			return artDawn
		case h >= 7.5 && h < 18.5:
			return artDay
		case h >= 18.5 && h < 20:
			return artDusk
		}
		return artNight
	}
	switch {
	case now.After(rise.Add(-40*time.Minute)) && now.Before(rise.Add(50*time.Minute)):
		return artDawn
	case now.After(set.Add(-50*time.Minute)) && now.Before(set.Add(40*time.Minute)):
		return artDusk
	case now.After(rise) && now.Before(set):
		return artDay
	}
	return artNight
}

// artFxFrame is how often the screen is drawn while rain or snow falls over the weather art: less often
// than the forecast page (fxFrame), since the art can be up all day, where the page is up for half a
// minute, and a frame of it costs more (the clock over it is drawn again each time).
const artFxFrame = 125 * time.Millisecond

// artKey is one landscape: the weather, the part of the day, the panel, and whose mountains.
type artKey struct {
	cond artCond
	when artTime
	w, h int
	seed uint64
}

// artCloud is one cloud: its soft shape, where it started, and how fast it drifts.
type artCloud struct {
	mask  *image.Alpha
	x, y  float64
	speed float64 // pixels a second
	col   color.RGBA
	alpha float64
}

// artLand is one landscape as drawn: the picture, its clouds, and the skyline the moon goes behind.
type artLand struct {
	key     artKey
	base    *image.RGBA
	clouds  []artCloud
	skyline []float64 // for each column, the top of the mountains and the hill in front

	sun     *image.NRGBA // the day's sun with its glow, drawn the first time it is needed
	moon    *image.NRGBA // the moon as last drawn for this landscape, kept while it looks the same
	moonKey moonSpriteKey
}

// artBodies is what moves across the art's sky: the sun by day, and the moon.
type artBodies struct {
	sunAlong float64 // through the day, 0 at sunrise and 1 at sunset
	sunOK    bool    // sunAlong is known: without it, the sun stays where it always was
	moon     artMoon // its south says which way both cross the sky
}

// weatherArt keeps the landscape in hand and the frame last composed from it.
type weatherArt struct {
	mu      sync.Mutex
	land    *artLand
	drawing bool

	frame   *image.RGBA
	frameAt int64  // the second frame was composed for
	gen     uint64 // counts the frames composed, so a kept copy of one knows when it is old
}

// arts is the one weather art a device keeps.
var arts weatherArt

// artFrame is the weather art for now, w by h: the landscape with the sun and the moon where they are
// (the moon from m) and the clouds where they have drifted to. nil until the first landscape is drawn,
// which happens off this goroutine; after that, a change of weather or of the part of the day shows the
// old one until the new one is ready.
func artFrame(now time.Time, cond string, rise, set time.Time, sunOK bool, m artMoon, w, h int) *image.RGBA {
	k := artKey{artCondFor(cond), artTimeFor(now, rise, set, sunOK), w, h, artSeed()}
	a := &arts
	a.mu.Lock()
	if (a.land == nil || k != a.land.key) && !a.drawing {
		a.drawing = true
		safe.Go("weather art", func() {
			start := time.Now()
			defer func() { // a landscape that failed to draw is tried again, rather than never
				a.mu.Lock()
				a.drawing = false
				a.mu.Unlock()
			}()
			land := paintLandscape(k)
			a.mu.Lock()
			// A new frame too: text laid over the art keeps its patches while the picture under it is
			// the same one (readable.go), and this is a different picture.
			a.land, a.frame, a.frameAt = land, nil, 0
			a.mu.Unlock()
			slog.Info("weather art drawn", "cond", k.cond, "time", k.when, "took", time.Since(start).Round(time.Millisecond))
		})
	}
	defer a.mu.Unlock()
	if a.land == nil || a.land.base.Rect.Dx() != w || a.land.base.Rect.Dy() != h {
		return nil
	}
	sec := now.Unix()
	if a.frame != nil && a.frameAt == sec {
		return a.frame
	}
	if a.frame == nil || a.frame.Rect != a.land.base.Rect {
		a.frame = image.NewRGBA(a.land.base.Rect)
	}
	b := artBodies{moon: m, sunOK: sunOK && set.After(rise)}
	if b.sunOK {
		b.sunAlong = float64(now.Sub(rise)) / float64(set.Sub(rise))
	}
	composeArt(a.frame, a.land, b, sec)
	a.frameAt = sec
	a.gen++
	return a.frame
}

// composeArt lays the sun, the moon and then the clouds over the landscape into frame: the sun and the
// moon where b has them, the clouds where they have drifted to at sec, so they pass in front.
func composeArt(frame *image.RGBA, land *artLand, b artBodies, sec int64) {
	copy(frame.Pix, land.base.Pix)
	drawSun(frame, land, b)
	drawMoon(frame, land, b.moon)
	t := float64(sec % 86400)
	w := float64(land.base.Rect.Dx())
	for _, c := range land.clouds {
		cw := float64(c.mask.Rect.Dx())
		x := math.Mod(c.x+c.speed*t, w+cw) - cw
		blendMask(frame, c.mask, int(x), int(c.y), c.col, c.alpha)
	}
}

// artSeed is this device's mountains: from its name, so they stay the same from day to day.
func artSeed() uint64 {
	h := fnv.New64a()
	h.Write([]byte(config.Get().Device.Name))
	return h.Sum64()
}

// The sky's colors: at the top, and at the horizon.
var artSky = map[artTime][2]color.RGBA{
	artDawn:  {{38, 44, 96, 255}, {250, 176, 140, 255}},
	artDay:   {{58, 128, 214, 255}, {178, 216, 240, 255}},
	artDusk:  {{52, 36, 92, 255}, {246, 132, 72, 255}},
	artNight: {{6, 10, 28, 255}, {26, 40, 78, 255}},
}

// artGray is an overcast sky's color at each part of the day.
var artGray = map[artTime]color.RGBA{
	artDawn: {120, 118, 132, 255}, artDay: {150, 160, 172, 255}, artDusk: {104, 94, 112, 255}, artNight: {30, 34, 44, 255},
}

// artHorizon is where the sky meets the land, as a part of the art's height.
const artHorizon = 0.62

// overcast says whether k's sky is covered: no sun, moon or stars show through it, except a dimmed sun
// or moon through fog.
func (k artKey) overcast() bool { return k.cond != artClear && k.cond != artPartly }

// skyColors is k's sky at the top and at the horizon.
func (k artKey) skyColors() (top, hor color.RGBA) {
	top, hor = artSky[k.when][0], artSky[k.when][1]
	if k.overcast() {
		gray := artGray[k.when]
		top, hor = lerp(top, gray, 0.7), lerp(hor, gray, 0.6)
	}
	if k.cond == artStorm {
		top, hor = lerp(top, color.RGBA{20, 22, 30, 255}, 0.5), lerp(hor, color.RGBA{40, 42, 52, 255}, 0.4)
	}
	return top, hor
}

// skyAt is k's sky color at height y.
func (k artKey) skyAt(y float64) color.RGBA {
	top, hor := k.skyColors()
	return lerp(top, hor, math.Pow(clamp01(y/(float64(k.h)*artHorizon)), 1.4))
}

// paintLandscape draws the landscape for k, and makes its clouds.
func paintLandscape(k artKey) *artLand {
	w, h := float64(k.w), float64(k.h)
	img := image.NewRGBA(image.Rect(0, 0, k.w, k.h))
	rng := rand.New(rand.NewPCG(k.seed, uint64(k.cond)<<8|uint64(k.when)))
	ridges := rand.New(rand.NewPCG(k.seed, 7)) // the mountains stay put whatever the weather
	unit := math.Max(w, h)

	top, hor := k.skyColors()
	overcast := k.overcast()
	gray := artGray[k.when]
	horizon := h * artHorizon
	for y := range k.h {
		draw.Draw(img, image.Rect(0, y, k.w, y+1), image.NewUniform(k.skyAt(float64(y))), image.Point{}, draw.Src)
	}

	// Stars on a clear night, and the sun low at dawn and dusk. The day's sun and the moon are not
	// drawn here: they move, so they are laid over the landscape as the art is shown (drawSun, drawMoon).
	switch {
	case k.when == artNight && !overcast:
		for range int(w * h / 1400) {
			b := uint8(120 + rng.IntN(136))
			artDisc(img, rng.Float64()*w, rng.Float64()*horizon*0.9, []float64{0.6, 0.8, 1.1}[rng.IntN(3)], color.RGBA{b, b, uint8(min(255, int(b)+20)), 255}, 1)
		}
	case (!overcast || k.cond == artFog) && (k.when == artDawn || k.when == artDusk):
		sx, sy, sr, sun := w*0.3, horizon-h*0.04, unit*0.036, color.RGBA{255, 200, 150, 255}
		if k.when == artDusk {
			sx, sy, sr, sun = w*0.68, horizon-h*0.05, unit*0.04, color.RGBA{255, 170, 90, 255}
		}
		artGlow(img, sx, sy, sr*4, sun, 0.35)
		if k.cond == artFog {
			sun = lerp(sun, hor, 0.5)
		}
		artDisc(img, sx, sy, sr, sun, 1)
	}

	// Three ranges of mountains, far to near, each nearer one darker.
	far := lerp(hor, top, 0.35)
	near := map[artTime]color.RGBA{artDawn: {44, 40, 70, 255}, artDay: {46, 84, 70, 255}, artDusk: {40, 28, 52, 255}, artNight: {10, 14, 26, 255}}[k.when]
	if overcast {
		near = lerp(near, gray, 0.35)
	}
	skyline := make([]float64, k.w)
	for x := range skyline {
		skyline[x] = h
	}
	rise := func(pts [][2]float64) {
		for x := range skyline {
			skyline[x] = math.Min(skyline[x], hillAt(pts, float64(x)+0.5))
		}
	}
	for i, l := range []struct{ base, rough, shade float64 }{
		{horizon - h*0.12, h * 0.1, 0.25}, {horizon - h*0.04, h * 0.08, 0.55}, {horizon + h*0.08, h * 0.05, 0.85},
	} {
		pts := ridge(ridges, w, l.base, l.rough, 9)
		rise(pts)
		col := lerp(far, near, l.shade)
		artFill(img, below(pts, h), col, 1)
		if k.cond == artSnow && i < 2 {
			// Snow on the peaks: the mountain's own shape, above a ragged line.
			line := l.base - l.rough*0.1
			for _, p := range pts {
				line = math.Min(line, p[1])
			}
			line += l.rough * 0.9
			cut := ridge(ridges, w, line, l.rough*0.35, 24)
			snow := lerp(color.RGBA{240, 244, 250, 255}, col, map[artTime]float64{artNight: 0.6, artDusk: 0.35}[k.when]+0.15)
			artFillAbove(img, below(pts, h), cut, snow)
		}
	}

	// The hill in front, and pines along it.
	ground := map[artTime]color.RGBA{artDawn: {28, 26, 44, 255}, artDay: {34, 62, 44, 255}, artDusk: {26, 18, 34, 255}, artNight: {6, 8, 16, 255}}[k.when]
	switch {
	case k.cond == artSnow:
		ground = lerp(lerp(color.RGBA{226, 232, 242, 255}, gray, 0.35), color.RGBA{20, 24, 36, 255}, map[artTime]float64{artNight: 0.6, artDusk: 0.3}[k.when])
	case overcast:
		ground = lerp(ground, gray, 0.25)
	}
	hill := ridge(ridges, w, h*0.86, h*0.04, 4)
	rise(hill)
	artFill(img, below(hill, h), ground, 1)
	tree := lerp(ground, color.RGBA{0, 0, 0, 255}, 0.45)
	if k.cond == artSnow {
		tree = lerp(near, color.RGBA{0, 0, 0, 255}, 0.3)
	}
	for range int(w / 26) {
		x := ridges.Float64() * w
		y := hillAt(hill, x) + h*0.012
		th := h * (0.07 + ridges.Float64()*0.07)
		for j := range 3 {
			tw, ty := th*(0.34-float64(j)*0.08), y-th*float64(j)*0.28
			artFill(img, [][2]float64{{x - tw, ty}, {x + tw, ty}, {x, ty - th*0.5}}, tree, 1)
		}
	}

	// Fog lies in bands across the valleys.
	if k.cond == artFog {
		band := image.NewAlpha(img.Rect)
		for i := range 5 {
			y := h * (0.5 + float64(i)*0.09)
			draw.Draw(band, image.Rect(0, int(y-h*0.02), k.w, int(y+h*0.02)), image.NewUniform(color.Alpha{120}), image.Point{}, draw.Src)
		}
		blurAlpha(band, int(h*0.025))
		blendMask(img, band, 0, 0, lerp(hor, color.RGBA{230, 230, 234, 255}, 0.5), 1)
	}

	// The clouds, made once; they drift over the landscape as it is shown.
	n := map[artCond]int{artClear: 0, artPartly: 3, artCloudy: 7, artRain: 7, artStorm: 7, artSnow: 6, artFog: 2}[k.cond]
	col := map[artTime]color.RGBA{artDawn: {250, 206, 196, 255}, artDay: {250, 252, 255, 255}, artDusk: {230, 150, 150, 255}, artNight: {60, 66, 90, 255}}[k.when]
	if overcast {
		col = lerp(col, gray, 0.5)
	}
	if k.cond == artStorm {
		col = color.RGBA{58, 60, 72, 255}
	}
	alpha := 0.84
	if overcast {
		alpha = 0.9
	}
	var clouds []artCloud
	for range n {
		scale := unit * (0.07 + rng.Float64()*0.05)
		if overcast {
			scale *= 1.3
		}
		clouds = append(clouds, artCloud{
			mask: cloudShape(scale), x: rng.Float64() * w, y: h*(0.08+rng.Float64()*0.27) - scale,
			speed: unit * (0.002 + rng.Float64()*0.003), col: col, alpha: alpha,
		})
	}
	return &artLand{key: k, base: img, clouds: clouds, skyline: skyline}
}

// ridge is a line of peaks across w, around base and within rough of it: a few points, each stretch
// between them split again with a smaller wobble, five times over.
func ridge(rng *rand.Rand, w, base, rough float64, n int) [][2]float64 {
	ys := make([]float64, n+1)
	for i := range ys {
		ys[i] = base + (rng.Float64()*2-1)*rough
	}
	for level := range 5 {
		amp := rough / math.Pow(2, float64(level+1))
		next := make([]float64, 0, len(ys)*2)
		for i := 0; i < len(ys)-1; i++ {
			next = append(next, ys[i], (ys[i]+ys[i+1])/2+(rng.Float64()*2-1)*amp)
		}
		ys = append(next, ys[len(ys)-1])
	}
	pts := make([][2]float64, len(ys))
	for i, y := range ys {
		pts[i] = [2]float64{w * float64(i) / float64(len(ys)-1), y}
	}
	return pts
}

// below is the shape under a ridge, down to h.
func below(pts [][2]float64, h float64) [][2]float64 {
	out := append([][2]float64{}, pts...)
	return append(out, [2]float64{pts[len(pts)-1][0], h}, [2]float64{0, h})
}

// hillAt is the ridge's height at x.
func hillAt(pts [][2]float64, x float64) float64 {
	for i := 1; i < len(pts); i++ {
		if pts[i][0] >= x {
			f := (x - pts[i-1][0]) / math.Max(pts[i][0]-pts[i-1][0], 1e-9)
			return pts[i-1][1] + (pts[i][1]-pts[i-1][1])*f
		}
	}
	return pts[len(pts)-1][1]
}

// artFill fills the polygon pts in c at alpha, anti-aliased; the rasterizer is only as big as the shape.
func artFill(img *image.RGBA, pts [][2]float64, c color.RGBA, alpha float64) {
	m := polygonMask(img.Rect, pts)
	if m == nil {
		return
	}
	blendMask(img, m, m.Rect.Min.X, m.Rect.Min.Y, c, alpha)
}

// artFillAbove fills the polygon pts in c where it is above the ragged line cut.
func artFillAbove(img *image.RGBA, pts, cut [][2]float64, c color.RGBA) {
	m := polygonMask(img.Rect, pts)
	if m == nil {
		return
	}
	for x := m.Rect.Min.X; x < m.Rect.Max.X; x++ {
		line := hillAt(cut, float64(x)) // once a column: the cut is hundreds of points long
		for y := max(m.Rect.Min.Y, int(math.Ceil(line))); y < m.Rect.Max.Y; y++ {
			m.SetAlpha(x, y, color.Alpha{})
		}
	}
	blendMask(img, m, m.Rect.Min.X, m.Rect.Min.Y, c, 1)
}

// polygonMask is the polygon's coverage within bounds, in a mask placed where it is.
func polygonMask(bounds image.Rectangle, pts [][2]float64) *image.Alpha {
	if len(pts) < 3 {
		return nil
	}
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range pts {
		x0, y0, x1, y1 = math.Min(x0, p[0]), math.Min(y0, p[1]), math.Max(x1, p[0]), math.Max(y1, p[1])
	}
	box := image.Rect(int(math.Floor(x0)), int(math.Floor(y0)), int(math.Ceil(x1))+1, int(math.Ceil(y1))+1).Intersect(bounds)
	if box.Empty() {
		return nil
	}
	z := vector.NewRasterizer(box.Dx(), box.Dy())
	z.MoveTo(float32(pts[0][0])-float32(box.Min.X), float32(pts[0][1])-float32(box.Min.Y))
	for _, p := range pts[1:] {
		z.LineTo(float32(p[0])-float32(box.Min.X), float32(p[1])-float32(box.Min.Y))
	}
	z.ClosePath()
	m := image.NewAlpha(box)
	z.Draw(m, box, image.Opaque, image.Point{})
	return m
}

// blendMask lays c over img through m (placed with its top left at x, y), at alpha.
func blendMask(img *image.RGBA, m *image.Alpha, x, y int, c color.RGBA, alpha float64) {
	off := image.Pt(x, y).Sub(m.Rect.Min)
	r := m.Rect.Add(off).Intersect(img.Rect)
	a := uint32(math.Round(alpha * 256))
	for py := r.Min.Y; py < r.Max.Y; py++ {
		mi := m.PixOffset(r.Min.X-off.X, py-off.Y)
		di := img.PixOffset(r.Min.X, py)
		for px := r.Min.X; px < r.Max.X; px, mi, di = px+1, mi+1, di+4 {
			k := uint32(m.Pix[mi]) * a >> 8
			if k == 0 {
				continue
			}
			d := img.Pix[di : di+3 : di+3]
			d[0] = uint8((uint32(d[0])*(255-k) + uint32(c.R)*k) / 255)
			d[1] = uint8((uint32(d[1])*(255-k) + uint32(c.G)*k) / 255)
			d[2] = uint8((uint32(d[2])*(255-k) + uint32(c.B)*k) / 255)
		}
	}
}

// drawSun lays the day's sun into frame on its arc from sunrise to sunset, behind land's mountains;
// at dawn and dusk it is low in the landscape itself. Dimmed through fog, hidden by an overcast sky.
func drawSun(frame *image.RGBA, land *artLand, b artBodies) {
	k := land.key
	if k.when != artDay || k.overcast() && k.cond != artFog {
		return
	}
	w, h := float64(k.w), float64(k.h)
	x, y := w*0.8, h*0.34
	if b.sunOK {
		x, y = skyPath(k, b.sunAlong, b.moon.south)
	}
	if land.sun == nil {
		r := math.Max(w, h) * 0.028
		sun := color.RGBA{255, 248, 220, 255}
		disc := sun
		if k.cond == artFog {
			_, hor := k.skyColors()
			disc = lerp(sun, hor, 0.5)
		}
		land.sun = sunSprite(r, sun, disc)
	}
	blendSprite(frame, land.skyline, land.sun, int(math.Round(x)), int(math.Round(y)))
}

// sunSprite is a sun of radius r, its disc in disc over a glow in glow four times as wide, centered
// on (0, 0): the same light artGlow and artDisc paint.
func sunSprite(r float64, glow, disc color.RGBA) *image.NRGBA {
	reach := int(r*4) + 1
	spr := image.NewNRGBA(image.Rect(-reach, -reach, reach+1, reach+1))
	for py := -reach; py <= reach; py++ {
		for px := -reach; px <= reach; px++ {
			d := math.Hypot(float64(px), float64(py))
			g := clamp01(1 - d/(r*4))
			ga := 0.35 * g * g
			da := clamp01(r - d + 0.5)
			a := da + ga*(1-da)
			if a <= 0 {
				continue
			}
			c := lerp(glow, disc, da/a)
			spr.SetNRGBA(px, py, color.NRGBA{c.R, c.G, c.B, uint8(math.Round(a * 255))})
		}
	}
	return spr
}

// artDisc is a soft-edged disc.
func artDisc(img *image.RGBA, cx, cy, r float64, c color.RGBA, alpha float64) {
	m := image.NewAlpha(image.Rect(int(cx-r-1), int(cy-r-1), int(cx+r+2), int(cy+r+2)))
	for y := m.Rect.Min.Y; y < m.Rect.Max.Y; y++ {
		for x := m.Rect.Min.X; x < m.Rect.Max.X; x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy) - r
			m.SetAlpha(x, y, color.Alpha{uint8(clamp01(0.5-d) * 255)})
		}
	}
	blendMask(img, m, m.Rect.Min.X, m.Rect.Min.Y, c, alpha)
}

// artGlow is light around the sun: strongest at its middle, gone at r.
func artGlow(img *image.RGBA, cx, cy, r float64, c color.RGBA, alpha float64) {
	m := image.NewAlpha(image.Rect(int(cx-r), int(cy-r), int(cx+r)+1, int(cy+r)+1))
	for y := m.Rect.Min.Y; y < m.Rect.Max.Y; y++ {
		for x := m.Rect.Min.X; x < m.Rect.Max.X; x++ {
			f := clamp01(1 - math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)/r) // clamped first: outside, squared, it would glow again
			m.SetAlpha(x, y, color.Alpha{uint8(f * f * 255)})
		}
	}
	blendMask(img, m, m.Rect.Min.X, m.Rect.Min.Y, c, alpha)
}

// cloudShape is a cloud's soft shape at scale: five puffs, blurred at the edges.
func cloudShape(scale float64) *image.Alpha {
	// Room around the puffs for their soft edges: a cloud cut off by its own canvas is a rectangle.
	w, h := int(scale*4.2), int(scale*2.4)
	m := image.NewAlpha(image.Rect(0, 0, w, h))
	cx, cy := float64(w)/2, float64(h)/2
	for _, p := range [][3]float64{{-1.1, 0.15, 0.55}, {-0.4, -0.2, 0.75}, {0.4, -0.1, 0.65}, {1.1, 0.2, 0.5}, {0, 0.25, 0.6}} {
		x, y, r := cx+p[0]*scale, cy+p[1]*scale, p[2]*scale
		for py := max(0, int(y-r)); py < min(h, int(y+r)+1); py++ {
			for px := max(0, int(x-r)); px < min(w, int(x+r)+1); px++ {
				if math.Hypot(float64(px)+0.5-x, float64(py)+0.5-y) <= r {
					m.Pix[py*m.Stride+px] = 255
				}
			}
		}
	}
	blurAlpha(m, int(scale*0.12))
	return m
}

// blurAlpha softens m in place: three box blurs of radius r across and down, which is close to a
// Gaussian and costs the same whatever r is.
func blurAlpha(m *image.Alpha, r int) {
	if r < 1 {
		return
	}
	w, h := m.Rect.Dx(), m.Rect.Dy()
	buf := make([]uint8, max(w, h))
	line := func(get func(i int) uint8, set func(i int, v uint8), n int) {
		for range 3 {
			for i := range n {
				buf[i] = get(i)
			}
			sum := 0
			for i := -r; i <= r; i++ {
				sum += int(buf[min(max(i, 0), n-1)])
			}
			for i := range n {
				set(i, uint8(sum/(2*r+1)))
				sum += int(buf[min(i+r+1, n-1)]) - int(buf[max(i-r, 0)])
			}
		}
	}
	for y := range h {
		row := m.Pix[y*m.Stride:]
		line(func(i int) uint8 { return row[i] }, func(i int, v uint8) { row[i] = v }, w)
	}
	for x := range w {
		line(func(i int) uint8 { return m.Pix[i*m.Stride+x] }, func(i int, v uint8) { m.Pix[i*m.Stride+x] = v }, h)
	}
}

// sceneArt is the weather art for a frame w by h at now, with the weather that moves over it; nil and
// fxNone while the slideshow shows photos.
func sceneArt(now time.Time, w, h int) (*image.RGBA, skyFx) {
	hm := home.Get()
	if !hm.SlideshowArt() {
		return nil, fxNone
	}
	cond := weatherNow(hm.Weather(), hm.Forecast(), now)
	rise, set, ok := hm.SunTimes(now)
	return artFrame(now, cond, rise, set, ok, moonNow(now), w, h), skyNow(cond)
}

// artVersion says whether img is the weather art's frame, and which one: it changes once a second at
// most, where the rain over it is drawn eight times a second, so a renderer can keep the frame with its
// wash laid on rather than washing the whole panel again each time.
func artVersion(img *image.RGBA) (uint64, bool) {
	a := &arts
	a.mu.Lock()
	defer a.mu.Unlock()
	if img == nil || img != a.frame {
		return 0, false
	}
	return a.gen, true
}

// washedArt keeps the weather art's frame with the wash on it, for the frames drawn in the same second.
type washedArt struct {
	img  *image.RGBA
	gen  uint64
	wash color.RGBA
}

// lay draws img washed with wash onto dst: from what is kept when img is the weather art's frame it
// has already washed, else washing it and keeping that when it is the art.
func (k *washedArt) lay(dst, img *image.RGBA, wash color.RGBA) {
	gen, art := artVersion(img)
	if art && k.img != nil && k.gen == gen && k.wash == wash && k.img.Rect == dst.Rect {
		copy(dst.Pix, k.img.Pix)
		return
	}
	draw.Draw(dst, dst.Rect, img, img.Bounds().Min, draw.Src)
	draw.Draw(dst, dst.Rect, image.NewUniform(wash), image.Point{}, draw.Over)
	if !art {
		return
	}
	if k.img == nil || k.img.Rect != dst.Rect {
		k.img = image.NewRGBA(dst.Rect)
	}
	copy(k.img.Pix, dst.Pix)
	k.gen, k.wash = gen, wash
}
