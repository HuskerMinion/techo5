package camera

import (
	"bytes"
	"image"
	"image/jpeg"
	"runtime"
	"testing"
)

func TestNativeImageStorageValidation(t *testing.T) {
	good := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	sub := image.NewRGBA(image.Rect(0, 0, 1024, 768)).SubImage(image.Rect(16, 16, 656, 376)).(*image.RGBA)
	for _, c := range []struct {
		name    string
		img     *image.RGBA
		quality int
		wantOK  bool
	}{
		{"sensor resolution", good, 85, true},
		{"subimage inherited stride", sub, 90, true},
		{"nil", nil, 85, false},
		{"too few rows", &image.RGBA{Pix: make([]byte, 64), Stride: 64, Rect: image.Rect(0, 0, 16, 16)}, 85, false},
		{"short stride", &image.RGBA{Pix: make([]byte, 2048), Stride: 32, Rect: image.Rect(0, 0, 16, 16)}, 85, false},
		{"negative stride", &image.RGBA{Pix: make([]byte, 2048), Stride: -1, Rect: image.Rect(0, 0, 16, 16)}, 85, false},
		{"unsupported width", &image.RGBA{Pix: make([]byte, 3000), Stride: 300, Rect: image.Rect(0, 0, 17, 16)}, 85, false},
		{"oversized", &image.RGBA{Rect: image.Rect(0, 0, 8208, 16)}, 85, false},
		{"invalid quality", good, 101, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := validateNativeRGBA(c.img, c.quality)
			if (err == nil) != c.wantOK {
				t.Errorf("validation: %v, want valid=%t", err, c.wantOK)
			}
		})
	}
}

func TestNativeEncoderRespectsWorkerBudget(t *testing.T) {
	before := runtime.GOMAXPROCS(0)
	t.Cleanup(func() { runtime.GOMAXPROCS(before) })
	for _, c := range []struct {
		budget, rows int
	}{
		{1, 45},
		{2, 45},
		{4, 45},
		{8, 45},
		{4, 1},
	} {
		runtime.GOMAXPROCS(c.budget)
		// The Echo Show 5 Gen 1 exposes two CPUs. Never assume a four-core
		// machine in a test that also runs on the physical ARM device.
		want := min(4, runtime.NumCPU(), runtime.GOMAXPROCS(0), c.rows)
		if got := nativeWorkers(c.rows); got != want {
			t.Errorf("CPUs=%d GOMAXPROCS=%d rows=%d: got %d workers; want %d",
				runtime.NumCPU(), c.budget, c.rows, got, want)
		}
		if got := nativeWorkers(c.rows); got > runtime.NumCPU() || got > runtime.GOMAXPROCS(0) || got > c.rows || got < 1 {
			t.Errorf("worker count %d exceeded CPU/process/row budget or fell below one", got)
		}
	}
}

// A real cropped RGBA image retains its parent's stride and starts at a
// nonzero rectangle origin. C must not read outside its visible rows.
func TestNativeTurboJPEGSubimageStride(t *testing.T) {
	parent := image.NewRGBA(image.Rect(0, 0, 1344, 768))
	for y := 0; y < 768; y++ {
		for x := 0; x < 1344; x++ {
			i := parent.PixOffset(x, y)
			parent.Pix[i], parent.Pix[i+1], parent.Pix[i+2], parent.Pix[i+3] =
				uint8((x+y)/4), uint8(y/3), uint8(x/6), 255
		}
	}
	sub := parent.SubImage(image.Rect(16, 16, 1296, 736)).(*image.RGBA)
	jpg, err := encodeNativeTurbo(sub, 85)
	if err != nil {
		t.Fatal(err)
	}
	dim, err := jpeg.DecodeConfig(bytes.NewReader(jpg))
	if err != nil {
		t.Fatal(err)
	}
	if dim.Width != 1280 || dim.Height != 720 {
		t.Fatalf("cropped native JPEG is %dx%d, want 1280x720", dim.Width, dim.Height)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(jpg))
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range []image.Point{{0, 0}, {640, 360}, {1279, 719}} {
		r, g, b, _ := decoded.At(point.X, point.Y).RGBA()
		if r == 0 && g == 0 && b == 0 {
			t.Errorf("unexpected blank pixel at %v", point)
		}
	}
}
