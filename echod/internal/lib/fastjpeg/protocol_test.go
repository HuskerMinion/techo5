package fastjpeg

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// The protocol's tests run the test binary itself as the helper: a script named as the helper sets
// fakeMode (the daemon starts the helper with an empty environment) and runs this binary, whose TestMain
// then answers as techo5-jpeg would, or as a broken one would.
const fakeMode = "FASTJPEG_FAKE_HELPER"

// fakeDir holds a copy of the test binary and the scripts, where anyone may run them: a test run as root
// starts the helper as nobody (confine), who can't reach the test's own temporary directories.
var fakeDir string

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeMode); mode != "" {
		os.Exit(fake(mode, os.Args[len(os.Args)-1]))
	}
	code := func() int {
		dir, err := os.MkdirTemp("", "fastjpeg-fake")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer os.RemoveAll(dir)
		exe, err := os.Executable()
		if err == nil {
			err = os.Chmod(dir, 0o755)
		}
		var b []byte
		if err == nil {
			b, err = os.ReadFile(exe)
		}
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, "fastjpeg.test"), b, 0o755)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fakeDir = dir
		return m.Run()
	}()
	os.Exit(code)
}

// fake is the helper's side: the JPEG on stdin, the denominator as the last argument, the picture on
// stdout, as tools/jpeg/techo5-jpeg.c writes it. image/jpeg decodes it; a smaller picture asked for is
// that decode's samples taken every d, which is enough to tell its size and layout apart.
func fake(mode, arg string) int {
	d, err := strconv.Atoi(arg)
	if err != nil || (d != 1 && d != 2 && d != 4 && d != 8) {
		fmt.Fprintln(os.Stderr, "techo5-jpeg: the denominator is 1, 2, 4 or 8")
		return 1
	}
	in, _ := io.ReadAll(os.Stdin)
	switch mode {
	case "decline":
		fmt.Fprintln(os.Stderr, "techo5-jpeg: not a YCbCr or gray JPEG")
		return 2
	case "refuse":
		fmt.Fprintln(os.Stderr, "techo5-jpeg: decoding: Corrupt JPEG data")
		return 1
	case "crash":
		os.Stdout.Write([]byte("T5J1"))
		return 139
	case "hang":
		time.Sleep(time.Minute)
		return 0
	}
	src, err := jpeg.Decode(bytes.NewReader(in))
	if err != nil {
		return 1
	}
	b := src.Bounds()
	w, h := scaled(b.Dx(), d), scaled(b.Dy(), d)
	var hdr [16]byte
	copy(hdr[:], "T5J1")
	binary.LittleEndian.PutUint32(hdr[4:], uint32(w))
	binary.LittleEndian.PutUint32(hdr[8:], uint32(h))
	var body []byte
	switch m := src.(type) {
	case *image.Gray:
		hdr[12] = 1
		g := image.NewGray(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				g.Pix[y*w+x] = m.GrayAt(b.Min.X+x*d, b.Min.Y+y*d).Y
			}
		}
		body = g.Pix
	case *image.YCbCr:
		hdr[12], hdr[13] = 2, byte(m.SubsampleRatio)
		o := image.NewYCbCr(image.Rect(0, 0, w, h), m.SubsampleRatio)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				sx, sy := b.Min.X+x*d, b.Min.Y+y*d
				o.Y[o.YOffset(x, y)] = m.Y[m.YOffset(sx, sy)]
				o.Cb[o.COffset(x, y)] = m.Cb[m.COffset(sx, sy)]
				o.Cr[o.COffset(x, y)] = m.Cr[m.COffset(sx, sy)]
			}
		}
		body = append(append(append(body, o.Y...), o.Cb...), o.Cr...)
	default:
		return 2
	}
	switch mode {
	case "magic":
		copy(hdr[:], "T5J0")
	case "size":
		binary.LittleEndian.PutUint32(hdr[4:], uint32(w+1))
	case "kind":
		hdr[12] = 7
	case "short":
		body = body[:len(body)/2]
	case "subsampling":
		hdr[13] = 9
	}
	os.Stdout.Write(hdr[:])
	os.Stdout.Write(body)
	return 0
}

// useFake makes the helper the test binary answering in mode.
func useFake(t *testing.T, mode string) {
	t.Helper()
	p := filepath.Join(fakeDir, "techo5-jpeg-"+mode)
	script := fmt.Sprintf("#!/bin/sh\n%s=%s exec %q \"$@\"\n", fakeMode, mode, filepath.Join(fakeDir, "fastjpeg.test"))
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	use(t, p, true)
}

func jpegOf(t *testing.T, m image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, m, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func sameDecode(t *testing.T, got, want image.Image) {
	t.Helper()
	if got.Bounds() != want.Bounds() {
		t.Fatalf("bounds %v, want %v", got.Bounds(), want.Bounds())
	}
	gp, wp := planes(got), planes(want)
	if len(gp) != len(wp) {
		t.Fatalf("%T, want %T", got, want)
	}
	for i := range wp {
		if !bytes.Equal(gp[i], wp[i]) {
			t.Fatalf("plane %d differs from image/jpeg's", i)
		}
	}
}

// The picture the helper writes is taken as it is: YCbCr at the JPEG's subsampling, or gray, the planes
// laid out as image.NewYCbCr lays them, and at the size the denominator gives.
func TestProtocolPicture(t *testing.T) {
	useFake(t, "ok")
	b, _ := os.ReadFile(synthetic(t))
	want, _ := jpeg.Decode(bytes.NewReader(b))
	got, err := helper(b, 1)
	if err != nil {
		t.Fatal(err)
	}
	sameDecode(t, got, want)
	if got.(*image.YCbCr).SubsampleRatio != want.(*image.YCbCr).SubsampleRatio {
		t.Errorf("subsampling %v, want %v", got.(*image.YCbCr).SubsampleRatio, want.(*image.YCbCr).SubsampleRatio)
	}

	gray := image.NewGray(image.Rect(0, 0, 61, 33))
	for i := range gray.Pix {
		gray.Pix[i] = uint8(i * 7)
	}
	gb := jpegOf(t, gray)
	gwant, _ := jpeg.Decode(bytes.NewReader(gb))
	ggot, err := helper(gb, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ggot.(*image.Gray); !ok {
		t.Fatalf("a gray JPEG came back %T", ggot)
	}
	sameDecode(t, ggot, gwant)

	for _, r := range []image.YCbCrSubsampleRatio{image.YCbCrSubsampleRatio444, image.YCbCrSubsampleRatio422, image.YCbCrSubsampleRatio420} {
		m := image.NewYCbCr(image.Rect(0, 0, 45, 29), r)
		for i := range m.Y {
			m.Y[i] = uint8(i)
		}
		got, err := helper(jpegOf(t, m), 1)
		if err != nil {
			t.Fatalf("%v: %v", r, err)
		}
		want, _ := jpeg.Decode(bytes.NewReader(jpegOf(t, m)))
		sameDecode(t, got, want)
	}
}

// DecodeAtLeast asks the helper for the denominator that still covers the size wanted, and takes the
// smaller picture back.
func TestProtocolDenominator(t *testing.T) {
	useFake(t, "ok")
	b, _ := os.ReadFile(synthetic(t)) // 333×217
	for _, c := range []struct{ w, h, dx, dy int }{
		{0, 0, 333, 217},
		{300, 200, 333, 217},
		{160, 100, 167, 109},
		{80, 50, 84, 55},
		{40, 20, 42, 28},
		{1, 1, 42, 28},
	} {
		got, err := DecodeAtLeast(b, c.w, c.h)
		if err != nil {
			t.Fatal(err)
		}
		if got.Bounds().Dx() != c.dx || got.Bounds().Dy() != c.dy {
			t.Errorf("at least %d×%d: %v, want %d×%d", c.w, c.h, got.Bounds(), c.dx, c.dy)
		}
	}
}

// Whatever goes wrong in the helper, the picture is image/jpeg's whole decode, and the helper's own
// answer says which kind of failure it was.
func TestProtocolFailures(t *testing.T) {
	old := helperWait
	helperWait = time.Second
	t.Cleanup(func() { helperWait = old })
	b, _ := os.ReadFile(synthetic(t))
	want, _ := jpeg.Decode(bytes.NewReader(b))
	for _, c := range []struct {
		mode     string
		declined bool
		refused  bool
	}{
		{"decline", true, false},
		{"refuse", false, true},
		{"crash", false, false},
		{"hang", false, false},
		{"magic", false, false},
		{"size", false, false},
		{"kind", false, false},
		{"short", false, false},
		{"subsampling", false, false},
	} {
		t.Run(c.mode, func(t *testing.T) {
			useFake(t, c.mode)
			start := time.Now()
			_, err := helper(b, 2)
			if err == nil {
				t.Fatal("the helper's answer was taken")
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Errorf("took %v", d)
			}
			if got := errors.Is(err, errDeclined); got != c.declined {
				t.Errorf("declined %v (%v), want %v", got, err, c.declined)
			}
			if got := errors.Is(err, errRefused); got != c.refused {
				t.Errorf("refused %v (%v), want %v", got, err, c.refused)
			}
			for _, f := range []func() (image.Image, error){
				func() (image.Image, error) { return Decode(b) },
				func() (image.Image, error) { return DecodeAtLeast(b, 40, 20) },
			} {
				got, err := f()
				if err != nil {
					t.Fatal(err)
				}
				sameDecode(t, got, want)
			}
		})
	}
}

// A picture that is not a JPEG never reaches the helper, and fails as image/jpeg fails it.
func TestProtocolNotAJPEG(t *testing.T) {
	useFake(t, "ok")
	if _, err := helper([]byte("<html>"), 1); err != errDeclined {
		t.Errorf("helper: %v, want it declined", err)
	}
	if _, err := Decode([]byte("<html>")); err == nil {
		t.Error("decoded what is not a JPEG")
	}
}
