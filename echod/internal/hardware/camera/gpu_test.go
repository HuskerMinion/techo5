//go:build linux && !dot && !spot && !panel

package camera

import (
	"fmt"
	"math/rand"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/gpu/gputest"
)

func TestMain(m *testing.M) {
	gputest.Main()
	m.Run()
}

// rawFrame is a frame of noise at the sensor's size, as the stream hands it out.
func rawFrame(seed int64) *Frame {
	g := streamed()
	raw := make([]byte, g.bpl*g.h)
	rand.New(rand.NewSource(seed)).Read(raw)
	return &Frame{raw: raw}
}

// Where frames are developed on the GPU, GPUYUYV sends the frame at the sensor's size, its bytes a line
// and its Bayer order, and hands back the helper's YUYV target: a dmabuf the encoder can read, w*2
// bytes a row here, with nothing read back. The target comes with the first reply of its size only,
// and the next frame gets the same one.
func TestGPUYUYVHandsBackTheGPUsBuffer(t *testing.T) {
	saved := gpuDevelops
	t.Cleanup(func() { gpuDevelops = saved })
	gpuDevelops = true
	log := gputest.Use(t, gputest.Config{})
	g := streamed()
	fd, stride, w, h, ok := rawFrame(1).GPUYUYV()
	if !ok || w != g.w || h != g.h || stride != g.w*2 || fd < 0 {
		t.Fatalf("GPUYUYV = fd %d, %d bytes a row, %dx%d, %v; want the %dx%d target", fd, stride, w, h, ok, g.w, g.h)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Size != int64(stride*h) {
		t.Fatalf("the target is %d bytes (%v), want %d", st.Size, err, stride*h)
	}
	fd2, _, _, _, ok := rawFrame(2).GPUYUYV()
	if !ok || fd2 != fd {
		t.Fatalf("the next frame: fd %d, %v; want the same target, fd %d", fd2, ok, fd)
	}
	devs := log.Ops("develop")
	blue := 0
	if sensor.blueFirst {
		blue = 1
	}
	want := fmt.Sprintf("develop %dx%d bpl=%d blue=%d mode=2", g.w, g.h, g.bpl, blue)
	if len(devs) != 2 || !strings.Contains(devs[0], want+" fds=3 new=true") || !strings.Contains(devs[1], want+" fds=0 new=false") {
		t.Fatalf("the helper was asked %q; want two developments %q, the first with its buffers", devs, want)
	}
}

// Without a frame to develop, on a board whose frames are not developed on the GPU, or with a helper
// that refuses, GPUYUYV says no, and the caller develops the frame on the cores.
func TestGPUYUYVDeclines(t *testing.T) {
	saved := gpuDevelops
	t.Cleanup(func() { gpuDevelops = saved })
	log := gputest.Use(t, gputest.Config{RefuseDevelop: syscall.EINVAL})

	gpuDevelops = false
	if _, _, _, _, ok := rawFrame(1).GPUYUYV(); ok || len(log.Lines()) != 0 {
		t.Fatalf("a board that does not develop on the GPU: ok=%v, the helper asked %q", ok, log.Lines())
	}
	gpuDevelops = true
	if _, _, _, _, ok := (&Frame{}).GPUYUYV(); ok || len(log.Ops("develop")) != 0 {
		t.Fatalf("a frame with nothing in it: ok=%v, the helper asked %q", ok, log.Ops("develop"))
	}
	if fd, _, _, _, ok := rawFrame(1).GPUYUYV(); ok || fd != -1 || len(log.Ops("develop")) != 1 {
		t.Fatalf("a helper that refuses: fd %d, ok=%v, asked %q", fd, ok, log.Ops("develop"))
	}
}
