//go:build !dot && !spot

package display

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log/slog"
	"math"
	"time"

	xdraw "golang.org/x/image/draw"
)

// The TECHO5 mark, drawn while the device comes up: the same picture the bootloader paints, so
// the screen does not change identity between power-on and the clock, with the signal arcs
// pulsing outward both ways until Home Assistant is listening.
//
//go:embed assets/techo5.png
var logoPNG []byte

const (
	// logoScale is how much larger than the file the mark is drawn; the file is 330×276.
	logoScale = 1.25

	// arcCentre is where the signal radiates from, in the file's own pixels: the middle of the
	// arcs drawn on the mark.
	arcCentreX, arcCentreY = 164, 105

	// The pulse: arcs leave the mark at arcFrom pixels from the centre and fade out by arcTo,
	// arcCount of them in flight, one full sweep every arcPeriod.
	arcFrom   = 100.0
	arcTo     = 380.0
	arcCount  = 3
	arcPeriod = 2400 * time.Millisecond
	arcWidth  = 5.0
	// arcSpread is how far above and below the horizontal the arcs reach, in radians.
	arcSpread = 38 * math.Pi / 180

	// waitingAfter is when the splash starts saying what it is waiting for: long enough that a
	// device which is already adopted is up and gone before it shows, short enough that nobody
	// sits watching a screen that looks stuck.
	waitingAfter = 12 * time.Second

	// splashMin is the least the splash is shown, so a fast connection still shows the mark.
	splashMin = 4 * time.Second
)

var (
	// navy is the mark's own background, so it sits on the screen without a rectangle.
	navy = color.RGBA{28, 31, 36, 255}
	teal = color.RGBA{20, 147, 180, 255}
)

// splash is the mark, scaled once, and where its arcs are centred on the canvas.
type splash struct {
	img      *image.RGBA
	at       image.Point // top-left on the canvas
	cx, cy   float64     // arc centre on the canvas
	loadedAt time.Time
}

func newSplash(w, h int) *splash {
	src, err := png.Decode(bytes.NewReader(logoPNG))
	if err != nil {
		slog.Error("decoding the logo failed", "err", err)
		return nil
	}
	sw := int(float64(src.Bounds().Dx()) * logoScale)
	sh := int(float64(src.Bounds().Dy()) * logoScale)
	img := image.NewRGBA(image.Rect(0, 0, sw, sh))
	xdraw.CatmullRom.Scale(img, img.Bounds(), src, src.Bounds(), draw.Src, nil)
	at := image.Pt((w-sw)/2, (h-sh)/2)
	return &splash{
		img: img, at: at,
		cx: float64(at.X) + arcCentreX*logoScale,
		cy: float64(at.Y) + arcCentreY*logoScale,
	}
}

// draw paints the splash for the moment t into the elapsed animation.
func (r *renderer) drawSplash(s *splash, elapsed time.Duration) {
	draw.Draw(r.dst, r.dst.Rect, image.NewUniform(navy), image.Point{}, draw.Src)
	if s == nil {
		r.text(r.title, "TECHO5", (r.w-r.width(r.title, "TECHO5"))/2, r.h/2, amber)
		return
	}
	r.arcs(s, elapsed)
	draw.Draw(r.dst, s.img.Bounds().Add(s.at), s.img, image.Point{}, draw.Over)
	if elapsed >= waitingAfter {
		r.splashWaiting(s)
	}
}

// splashWaiting says what the splash is waiting for, once it has been up long enough that waiting is
// the explanation.
//
// The mark stays until Home Assistant subscribes, which does not happen until somebody accepts the
// device there. Between flashing a unit and adopting it that can be minutes, or as long as it takes
// to walk to a computer, and the screen said nothing at all - so it reads as a device that has hung
// on its first boot. Somebody sat in front of one for ten minutes before finding out that accepting
// the ESPHome prompt was what freed it (techo5-checkers issue #2). It costs two lines to say so.
//
// Not said from the first frame: a device that is adopted already reaches Home Assistant in a couple
// of seconds, and telling that owner to go and do something they did not need to do would be worse
// than saying nothing.
func (r *renderer) splashWaiting(s *splash) {
	const (
		what  = "Waiting for Home Assistant"
		where = "Settings > Devices & services > ESPHome"
	)
	// Below the mark, which reaches within about seventy pixels of the bottom on this panel: the two
	// lines go in what is left rather than over the wordmark. If a panel ever leaves less room than
	// they need, they sit on the bottom edge instead of climbing onto the mark.
	const gap, lead = 26, 32
	y := s.at.Y + s.img.Bounds().Dy() + gap
	if bottom := r.h - 6; y+lead > bottom {
		y = bottom - lead
	}
	r.text(r.tiny, what, (r.w-r.width(r.tiny, what))/2, y, teal)
	r.text(r.tiny, where, (r.w-r.width(r.tiny, where))/2, y+lead, dim)
}

// arcs draws arcCount rings expanding from the mark to both sides, each fading as it travels.
// Only the band the arcs can reach is scanned, and only pixels near a ring are touched.
func (r *renderer) arcs(s *splash, elapsed time.Duration) {
	phase := math.Mod(elapsed.Seconds()/arcPeriod.Seconds(), 1)
	var radii [arcCount]float64
	var fades [arcCount]float64
	for i := range arcCount {
		p := math.Mod(phase+float64(i)/arcCount, 1)
		radii[i] = arcFrom + p*(arcTo-arcFrom)
		fades[i] = (1 - p) * (1 - p) // brighter near the mark, gone at the edge
	}
	x0 := max(int(s.cx-arcTo-arcWidth), 0)
	x1 := min(int(s.cx+arcTo+arcWidth), r.w-1)
	y0 := max(int(s.cy-arcTo*math.Sin(arcSpread)-arcWidth), 0)
	y1 := min(int(s.cy+arcTo*math.Sin(arcSpread)+arcWidth), r.h-1)
	pix := r.dst.Pix
	for y := y0; y <= y1; y++ {
		dy := float64(y) - s.cy
		for x := x0; x <= x1; x++ {
			dx := float64(x) - s.cx
			if math.Abs(dy) > math.Abs(dx)*math.Tan(arcSpread) {
				continue // outside the two sideways cones
			}
			d := math.Hypot(dx, dy)
			for i := range arcCount {
				w := math.Abs(d - radii[i])
				if w > arcWidth {
					continue
				}
				a := (1 - w/arcWidth) * fades[i]
				// soften the cone edges
				edge := 1 - math.Abs(dy)/(math.Abs(dx)*math.Tan(arcSpread)+1e-9)
				a *= math.Min(edge*4, 1)
				o := r.dst.PixOffset(x, y)
				pix[o] = blend(pix[o], teal.R, a)
				pix[o+1] = blend(pix[o+1], teal.G, a)
				pix[o+2] = blend(pix[o+2], teal.B, a)
			}
		}
	}
}

func blend(under, over uint8, a float64) uint8 {
	return uint8(float64(under)*(1-a) + float64(over)*a + 0.5)
}
