//go:build linux && hwdevice && !dot && !spot && !panel

package camera

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"math"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/gpu"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/jpegenc"
)

// childCPU is the CPU time so far of this process's children: the GPU helper it started.
func childCPU() time.Duration {
	var total time.Duration
	self := strconv.Itoa(os.Getpid())
	dirs, _ := os.ReadDir("/proc")
	for _, d := range dirs {
		b, err := os.ReadFile("/proc/" + d.Name() + "/stat")
		if err != nil {
			continue
		}
		// pid (comm) state ppid ... utime stime: fields 14 and 15, counted after the comm's ')'.
		f := strings.Fields(string(b[bytes.LastIndexByte(b, ')')+2:]))
		if len(f) < 13 || f[1] != self {
			continue
		}
		u, _ := strconv.Atoi(f[11])
		s, _ := strconv.Atoi(f[12])
		total += time.Duration(u+s) * time.Second / 100 // USER_HZ
	}
	return total
}

// selfCPU is this process's CPU time so far, user and system.
func selfCPU() time.Duration {
	var ru syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

// psnr is how close two pictures of a size are, in dB.
func psnr(a, b image.Image) float64 {
	var sum float64
	r := a.Bounds()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			r0, g0, b0, _ := a.At(x, y).RGBA()
			r1, g1, b1, _ := b.At(x, y).RGBA()
			for _, d := range []float64{float64(r0>>8) - float64(r1>>8), float64(g0>>8) - float64(g1>>8), float64(b0>>8) - float64(b1>>8)} {
				sum += d * d
			}
		}
	}
	mse := sum / float64(3*r.Dx()*r.Dy())
	return 10 * math.Log10(255*255/mse)
}

// On a device with the GPU helper and the JPEG encoder only: a frame of the sensor's size, developed
// and encoded the two ways the camera pages can, on the cores into the encoder's buffer (FullYUYV,
// EncodeYUYV) and on the GPU into a buffer the encoder reads in place (GPUYUYV, EncodeDMABuf). Both
// JPEGs decode at the frame's size and show the same picture. The time and CPU each took are logged,
// the GPU helper's CPU counted with the test's. TECHO5_GPU_HELPER runs another build of the helper.
// It needs no camera: the daemon can keep it.
func TestDeviceGPUYUYVEncodesInPlace(t *testing.T) {
	if !jpegenc.Available() {
		t.Skip("no mtk-jpeg-enc on this board")
	}
	if p := os.Getenv("TECHO5_GPU_HELPER"); p != "" {
		gpu.SetHelperPath(p)
	}
	saved := gpuDevelops
	t.Cleanup(func() { gpuDevelops = saved; gpu.Stop(errors.New("the test is over")) })
	gpuDevelops = true
	if !gpu.Available() {
		t.Skip("no GPU helper")
	}
	t.Logf("GPU: %s", gpu.Name())
	raw := synthRaw()
	g := streamed()
	const q, n = 80, 20

	// Warm both ways up: the sessions open, the helper makes its target.
	if _, _, _, _, ok := (&Frame{raw: raw}).GPUYUYV(); !ok {
		t.Fatal("the GPU did not develop the frame")
	}
	type way struct {
		name      string
		wall, cpu time.Duration
		bytes     int
		jpg       []byte
	}
	cores := &way{name: "cores (FullYUYV, EncodeYUYV)"}
	onGPU := &way{name: "GPU (GPUYUYV, EncodeDMABuf)"}
	for i := range n + 1 {
		for _, w := range []*way{cores, onGPU} {
			f := &Frame{raw: raw} // not developed yet: the development is part of the cost
			start, c := time.Now(), selfCPU()+childCPU()
			var b []byte
			var err error
			if w == cores {
				developed := false
				b, err = jpegenc.EncodeYUYV(g.w, g.h, q, func(dst []byte) { developed = f.FullYUYV(dst) })
				if err == nil && !developed {
					t.Fatal("FullYUYV did not develop the frame")
				}
			} else {
				fd, stride, fw, fh, ok := f.GPUYUYV()
				if !ok {
					t.Fatal("the GPU did not develop the frame")
				}
				if i == 0 {
					t.Logf("the GPU's buffer: %dx%d, %d bytes a row", fw, fh, stride)
				}
				b, err = jpegenc.EncodeDMABuf(fd, fw, fh, stride, q)
			}
			if err != nil {
				t.Fatalf("%s: %v", w.name, err)
			}
			if i == 0 {
				continue // warm-up
			}
			w.wall += time.Since(start)
			w.cpu += selfCPU() + childCPU() - c
			w.bytes += len(b)
			w.jpg = b
		}
	}
	var pics [2]image.Image
	for i, w := range []*way{cores, onGPU} {
		img, err := jpeg.Decode(bytes.NewReader(w.jpg))
		if err != nil || img.Bounds() != image.Rect(0, 0, g.w, g.h) {
			t.Fatalf("%s: decodes as %v, %v", w.name, img.Bounds(), err)
		}
		pics[i] = img
		t.Logf("%s: %v a picture, %v CPU, %d bytes", w.name, w.wall/n, w.cpu/n, w.bytes/n)
	}
	p := psnr(pics[0], pics[1])
	t.Logf("the two pictures: %.1f dB apart", p)
	if p < 35 {
		t.Errorf("the GPU's picture is %.1f dB from the cores', want at least 35", p)
	}
}
