package picscale

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"math/rand/v2"
	"testing"

	xdraw "golang.org/x/image/draw"
)

// scene is a synthetic photo: smooth color gradients, hard edges and fine detail, the things a
// scaler gets wrong in different ways.
func scene(w, h int, ratio image.YCbCrSubsampleRatio) *image.YCbCr {
	m := image.NewYCbCr(image.Rect(0, 0, w, h), ratio)
	rng := rand.New(rand.NewPCG(1, 2))
	for y := range h {
		for x := range w {
			fx, fy := float64(x)/float64(w), float64(y)/float64(h)
			v := 100 + 80*math.Sin(9*fx+4*fy) + 30*math.Sin(float64(x*x+y*y)/float64(w*8))
			if (x/97+y/61)%2 == 0 {
				v += 40
			}
			v += float64(rng.IntN(9)) - 4
			m.Y[m.YOffset(x, y)] = byte(min(max(v, 0), 255))
		}
	}
	for y := range h {
		for x := range w {
			o := m.COffset(x, y)
			fx, fy := float64(x)/float64(w), float64(y)/float64(h)
			m.Cb[o] = byte(128 + 25*math.Sin(5*fx-3*fy))
			m.Cr[o] = byte(128 + 25*math.Cos(4*fx+6*fy))
		}
	}
	return m
}

func toRGBAImage(src image.Image) *image.RGBA {
	m := image.NewRGBA(src.Bounds())
	draw.Draw(m, m.Rect, src, src.Bounds().Min, draw.Src)
	return m
}

func psnr(a, b *image.RGBA) float64 {
	var se float64
	var n int
	for i := 0; i < len(a.Pix); i += 4 {
		for c := range 3 {
			d := float64(a.Pix[i+c]) - float64(b.Pix[i+c])
			se += d * d
			n++
		}
	}
	if se == 0 {
		return 99
	}
	return 10 * math.Log10(255*255/(se/float64(n)))
}

// maxDiff is the largest difference between any two samples.
func maxDiff(a, b *image.RGBA) int {
	d := 0
	for i := range a.Pix {
		d = max(d, int(a.Pix[i])-int(b.Pix[i]), int(b.Pix[i])-int(a.Pix[i]))
	}
	return d
}

// cover is the target that scales a sw×sh picture to cover w×h, centered, as the slideshow crops it.
func cover(sw, sh, w, h int) image.Rectangle {
	tw, th := w, sh*w/sw
	if th < h {
		tw, th = sw*h/sh, h
	}
	return image.Rect((w-tw)/2, (h-th)/2, (w-tw)/2+tw, (h-th)/2+th)
}

// Against xdraw.BiLinear, which is the same filter in floating point: a sample may round the other
// way, and nothing should be further off than that, whatever the picture's kind, the scale or the crop.
func TestMatchesXDraw(t *testing.T) {
	for _, k := range []struct {
		k   *Kernel
		ref *xdraw.Kernel
	}{{BiLinear, xdraw.BiLinear}, {CatmullRom, xdraw.CatmullRom}} {
		matchesXDraw(t, k.k, k.ref)
	}
}

func matchesXDraw(t *testing.T, k *Kernel, ref *xdraw.Kernel) {
	base := scene(400, 300, image.YCbCrSubsampleRatio444)
	gray := image.NewGray(base.Rect)
	copy(gray.Pix, base.Y)
	rgba := toRGBAImage(base)
	for _, src := range []image.Image{gray, rgba, rgba.SubImage(image.Rect(13, 7, 391, 288))} {
		for _, size := range [][2]int{{400, 300}, {400, 150}, {200, 300}, {399, 301}, {200, 150}, {123, 77}, {37, 300}, {900, 610}, {64, 64}, {1, 1}} {
			sb := src.Bounds()
			for _, dr := range []image.Rectangle{image.Rect(0, 0, size[0], size[1]), cover(sb.Dx(), sb.Dy(), size[0], size[1])} {
				name := fmt.Sprintf("%T %v to %v (%.0f)", src, sb, dr, k.radius)
				want := image.NewRGBA(image.Rect(0, 0, size[0], size[1]))
				got := image.NewRGBA(want.Rect)
				ref.Scale(want, dr, src, sb, draw.Src, nil)
				if !k.scale(got, dr, src, sb, draw.Src) {
					t.Fatalf("%s: not taken", name)
				}
				if d := maxDiff(got, want); d > 1 {
					t.Errorf("%s: off by %d", name, d)
				}
			}
		}
	}
}

// A YCbCr picture's chroma is interpolated between samples, where x/image/draw repeats each one over
// the pixels it covers, and is turned into color after it is filtered rather than before, which only
// differs where a color is out of range and clipped. So the two are held to being close, not the same.
func TestYCbCr(t *testing.T) {
	for _, ratio := range []image.YCbCrSubsampleRatio{
		image.YCbCrSubsampleRatio444, image.YCbCrSubsampleRatio422, image.YCbCrSubsampleRatio420,
		image.YCbCrSubsampleRatio440, image.YCbCrSubsampleRatio411, image.YCbCrSubsampleRatio410,
	} {
		src := scene(401, 301, ratio)
		for _, dr := range []image.Rectangle{image.Rect(0, 0, 401, 301), image.Rect(0, 0, 320, 200), cover(401, 301, 320, 200), image.Rect(-5, 3, 900, 700)} {
			want := image.NewRGBA(image.Rect(0, 0, 320, 200))
			got := image.NewRGBA(want.Rect)
			xdraw.CatmullRom.Scale(want, dr, src, src.Rect, draw.Src, nil)
			if !CatmullRom.scale(got, dr, src, src.Rect, draw.Over) {
				t.Fatalf("%v: not taken", ratio)
			}
			if p := psnr(got, want); p < 40 {
				t.Errorf("%v to %v: %.1f dB from xdraw.CatmullRom", ratio, dr, p)
			}
		}
	}
}

// The draw.Over that the slideshow uses is only taken for a picture with nothing to see through,
// and a destination outside the target is left alone.
func TestOverAndClip(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 10, 10))
	draw.Draw(src, src.Rect, image.NewUniform(color.RGBA{10, 20, 30, 255}), image.Point{}, draw.Src)
	dst := image.NewRGBA(image.Rect(0, 0, 20, 20))
	if !CatmullRom.scale(dst, image.Rect(5, 5, 15, 15), src, src.Rect, draw.Over) {
		t.Fatal("an opaque RGBA picture: not taken")
	}
	if dst.RGBAAt(4, 4) != (color.RGBA{}) || dst.RGBAAt(15, 15) != (color.RGBA{}) || dst.RGBAAt(5, 5) != (color.RGBA{10, 20, 30, 255}) {
		t.Errorf("drew outside the target, or not inside it: %v %v %v", dst.RGBAAt(4, 4), dst.RGBAAt(15, 15), dst.RGBAAt(5, 5))
	}
	src.Pix[3] = 128
	if CatmullRom.scale(dst, dst.Rect, src, src.Rect, draw.Over) {
		t.Error("a see-through picture drawn Over: taken")
	}
	if !CatmullRom.scale(dst, dst.Rect, src, src.Rect, draw.Src) {
		t.Error("a see-through picture drawn Src: not taken")
	}
	if CatmullRom.scale(dst, dst.Rect, image.NewNRGBA(src.Rect), src.Rect, draw.Src) {
		t.Error("an NRGBA picture: taken")
	}
	if CatmullRom.scale(dst, dst.Rect, src, image.Rect(0, 0, 11, 10), draw.Src) {
		t.Error("a source rectangle beyond the picture: taken")
	}
}

// Quality, against a CatmullRom reference from the whole picture: never worse than ApproxBiLinear,
// which this replaces.
func TestQuality(t *testing.T) {
	full := scene(2016, 1512, image.YCbCrSubsampleRatio420)
	for _, size := range [][2]int{{1280, 800}, {960, 480}, {640, 400}, {1600, 1200}} {
		w, h := size[0], size[1]
		dr := cover(full.Rect.Dx(), full.Rect.Dy(), w, h)
		ref := image.NewRGBA(image.Rect(0, 0, w, h))
		xdraw.CatmullRom.Scale(ref, dr, full, full.Rect, draw.Src, nil)
		old := image.NewRGBA(ref.Rect)
		xdraw.ApproxBiLinear.Scale(old, dr, full, full.Rect, draw.Src, nil)
		got := image.NewRGBA(ref.Rect)
		CatmullRom.Scale(got, dr, full, full.Rect, draw.Src)
		po, pn := psnr(old, ref), psnr(got, ref)
		t.Logf("%d×%d: ApproxBiLinear %.1f dB, picscale %.1f dB", w, h, po, pn)
		if pn < po-0.1 {
			t.Errorf("%d×%d: %.1f dB, below ApproxBiLinear's %.1f", w, h, pn, po)
		}
	}
}

// The pictures the panels are fitted from: an Immich preview, a 12 megapixel photo decoded at a half
// and a quarter, and a 1080p camera frame.
var benchCases = []struct {
	name   string
	sw, sh int
	w, h   int
	rgba   bool
}{
	{"preview-1280x800", 1440, 1080, 1280, 800, false},
	{"preview-960x480", 1440, 1080, 960, 480, false},
	{"photo-half-1280x800", 2016, 1512, 1280, 800, false},
	{"photo-quarter-960x480", 1008, 756, 960, 480, false},
	{"camera-rgba-1280x720", 1920, 1080, 1280, 720, true},
}

func BenchmarkFit(b *testing.B) {
	for _, c := range benchCases {
		var src image.Image = scene(c.sw, c.sh, image.YCbCrSubsampleRatio420)
		if c.rgba {
			src = toRGBAImage(src)
		}
		dst := image.NewRGBA(image.Rect(0, 0, c.w, c.h))
		dr := cover(c.sw, c.sh, c.w, c.h)
		b.Run(c.name+"/approxbilinear", func(b *testing.B) {
			for b.Loop() {
				xdraw.ApproxBiLinear.Scale(dst, dr, src, src.Bounds(), draw.Over, nil)
			}
		})
		b.Run(c.name+"/picscale", func(b *testing.B) {
			for b.Loop() {
				CatmullRom.Scale(dst, dr, src, src.Bounds(), draw.Over)
			}
		})
	}
}
