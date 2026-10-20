package fastjpeg

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The tests that run the helper need one built for this machine: TECHO5_JPEG_HELPER names it
// (tools/jpeg/build.sh's bin/techo5-jpeg-arm64, in an arm64 container). Without it they are skipped,
// and only the fallback is tested.
func withHelper(t *testing.T) {
	t.Helper()
	p := os.Getenv("TECHO5_JPEG_HELPER")
	if p == "" {
		t.Skip("TECHO5_JPEG_HELPER is not set")
	}
	use(t, p, true)
}

func use(t *testing.T, path string, ok bool) {
	oldPath, oldPresent := helperPath, present
	helperPath, present = path, func() bool { return ok }
	t.Cleanup(func() { helperPath, present = oldPath, oldPresent })
}

// testdata is Go's own set of JPEGs, every subsampling image/jpeg reads, baseline and progressive.
func testdata(t *testing.T) []string {
	files, _ := filepath.Glob(filepath.Join(runtime.GOROOT(), "src", "image", "testdata", "video-00*.jpeg"))
	if len(files) == 0 {
		t.Skip("no GOROOT/src/image/testdata")
	}
	return append(files, synthetic(t))
}

// synthetic is a picture with detail at every scale, written by image/jpeg (4:2:0).
func synthetic(t *testing.T) string {
	img := image.NewRGBA(image.Rect(0, 0, 333, 217)) // odd sizes, to cover the partial blocks
	for y := 0; y < 217; y++ {
		for x := 0; x < 333; x++ {
			f := float64(x*x+y*y) / 900
			img.Set(x, y, color.RGBA{uint8(128 + 127*math.Sin(f)), uint8(x ^ y), uint8(y * 255 / 216), 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "synthetic.jpeg")
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// planes is a decoded picture's samples, row by row, cropped to its bounds.
func planes(m image.Image) [][]byte {
	switch m := m.(type) {
	case *image.Gray:
		var out []byte
		for y := m.Rect.Min.Y; y < m.Rect.Max.Y; y++ {
			out = append(out, m.Pix[m.PixOffset(m.Rect.Min.X, y):m.PixOffset(m.Rect.Max.X, y)]...)
		}
		return [][]byte{out}
	case *image.YCbCr:
		var y0, cb, cr []byte
		b := m.Rect
		for y := b.Min.Y; y < b.Max.Y; y++ {
			y0 = append(y0, m.Y[m.YOffset(b.Min.X, y):m.YOffset(b.Max.X-1, y)+1]...)
		}
		// The chroma rows each cover, at the picture's subsampling.
		seen := map[int]bool{}
		for y := b.Min.Y; y < b.Max.Y; y++ {
			o := m.COffset(b.Min.X, y)
			if seen[o] {
				continue
			}
			seen[o] = true
			e := m.COffset(b.Max.X-1, y) + 1
			cb, cr = append(cb, m.Cb[o:e]...), append(cr, m.Cr[o:e]...)
		}
		return [][]byte{y0, cb, cr}
	}
	return nil
}

func TestMatchesImageJPEG(t *testing.T) {
	withHelper(t)
	for _, f := range testdata(t) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(f)
		want, werr := jpeg.Decode(bytes.NewReader(b))
		got, gerr := Decode(b)
		if (werr == nil) != (gerr == nil) {
			t.Errorf("%s: image/jpeg says %v, Decode %v", name, werr, gerr)
			continue
		}
		if werr != nil {
			continue
		}
		if got.Bounds() != want.Bounds() {
			t.Errorf("%s: bounds %v, want %v", name, got.Bounds(), want.Bounds())
			continue
		}
		helped, err := helper(b, 1)
		if strings.Contains(name, "cmyk") || strings.Contains(name, "rgb") || strings.Contains(name, "410") {
			if err == nil {
				t.Errorf("%s: the helper decoded what it should hand to image/jpeg", name)
			}
		} else if err != nil {
			t.Errorf("%s: the helper failed: %v", name, err)
		}
		if strings.Contains(name, "truncated") {
			// A progressive JPEG missing its last scans: libjpeg smooths the coefficients those scans would
			// have refined (block smoothing), where image/jpeg leaves them zero, so the two differ by more
			// than rounding. Only the size is compared.
			continue
		}
		gp, wp := planes(got), planes(want)
		if len(gp) != len(wp) {
			t.Errorf("%s: %T, want %T", name, got, want)
			continue
		}
		var off, n int
		for i := range wp {
			if len(gp[i]) != len(wp[i]) {
				t.Fatalf("%s: plane %d has %d samples, want %d", name, i, len(gp[i]), len(wp[i]))
			}
			for j := range wp[i] {
				d := int(gp[i][j]) - int(wp[i][j])
				if d < -1 || d > 1 {
					t.Fatalf("%s: plane %d sample %d is %d, image/jpeg's %d", name, i, j, gp[i][j], wp[i][j])
				}
				if d != 0 {
					off++
				}
				n++
			}
		}
		t.Logf("%s: %T via helper=%v, %d of %d samples off by one (%.2f%%)", name, got, err == nil && helped != nil, off, n, 100*float64(off)/float64(n))
	}
}

// TestScaled checks that a smaller decode is the picture made smaller: within a few levels of image/jpeg's
// whole decode averaged over the same blocks.
func TestScaled(t *testing.T) {
	withHelper(t)
	for _, f := range testdata(t) {
		b, _ := os.ReadFile(f)
		name := filepath.Base(f)
		full, err := jpeg.Decode(bytes.NewReader(b))
		if err != nil {
			continue
		}
		fy, ok := full.(*image.YCbCr)
		if !ok {
			continue
		}
		for _, d := range []int{2, 4, 8} {
			got, err := helper(b, d)
			if err != nil {
				if err == errDeclined {
					break
				}
				t.Fatalf("%s 1/%d: %v", name, d, err)
			}
			gy := got.(*image.YCbCr)
			fb := fy.Bounds()
			if w, h := scaled(fb.Dx(), d), scaled(fb.Dy(), d); gy.Rect.Dx() != w || gy.Rect.Dy() != h {
				t.Fatalf("%s 1/%d: %v, want %dx%d", name, d, gy.Rect, w, h)
			}
			var se float64
			var n int
			for y := 0; y+1 < gy.Rect.Dy() && (y+1)*d <= fb.Dy(); y++ {
				for x := 0; (x+1)*d <= fb.Dx(); x++ {
					var s int
					for yy := 0; yy < d; yy++ {
						for xx := 0; xx < d; xx++ {
							s += int(fy.Y[fy.YOffset(x*d+xx, y*d+yy)])
						}
					}
					e := float64(gy.Y[gy.YOffset(x, y)]) - float64(s)/float64(d*d)
					se += e * e
					n++
				}
			}
			psnr := 10 * math.Log10(255*255/(se/float64(n)))
			t.Logf("%s 1/%d: luma %.1f dB against the whole decode box-averaged", name, d, psnr)
			if psnr < 35 {
				t.Errorf("%s 1/%d: luma only %.1f dB from the box-averaged whole decode", name, d, psnr)
			}
		}
	}
}

// TestFallback is image/jpeg exactly when there is no helper, at any size asked for.
func TestFallback(t *testing.T) {
	use(t, "/nonexistent/techo5-jpeg", true) // as if it were there, and failed to start
	p := synthetic(t)
	b, _ := os.ReadFile(p)
	want, _ := jpeg.Decode(bytes.NewReader(b))
	for _, f := range []func() (image.Image, error){
		func() (image.Image, error) { return Decode(b) },
		func() (image.Image, error) { return DecodeAtLeast(b, 100, 50) },
	} {
		got, err := f()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.(*image.YCbCr).Y, want.(*image.YCbCr).Y) || got.Bounds() != want.Bounds() {
			t.Fatal("the fallback is not image/jpeg's decode")
		}
	}
	if _, err := Decode([]byte("not a jpeg")); err == nil {
		t.Fatal("decoded what is not a JPEG")
	}
}

func TestDenominator(t *testing.T) {
	for _, c := range []struct{ sw, sh, w, h, d int }{
		{4032, 3024, 1280, 800, 2},
		{4032, 3024, 960, 480, 4},
		{3024, 4032, 1280, 800, 2},
		{1440, 1080, 1280, 800, 1},
		{1440, 1080, 960, 480, 1},
		{1920, 1080, 960, 480, 2},
		{8000, 6000, 960, 480, 8},
		{100, 100, 0, 0, 8}, // DecodeAtLeast never asks for these
		{1009, 757, 127, 95, 8},
		{1009, 757, 128, 95, 4},
	} {
		if d := Denominator(c.sw, c.sh, c.w, c.h); d != c.d {
			t.Errorf("Denominator(%d, %d, %d, %d) = %d, want %d", c.sw, c.sh, c.w, c.h, d, c.d)
		}
	}
}
