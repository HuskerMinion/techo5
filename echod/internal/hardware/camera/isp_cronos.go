//go:build !dot && !spot

package camera

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

// The 2nd gen Show 5's ISP develops a frame for the stream: the MT8163's pass 2, which the board's
// mainline kernel drives (custom-kernel/cronos, patches/mt8163-cronos-camera.c, CRONOS_IOC_DEVELOP on
// the camera's own node; research/mt8163-isp-pass2.md has how it was found). The frame goes in packed,
// as the sensor sent it, with its tone; I420 comes back: black level, white balance and the stretch to
// white per Bayer channel, a real demosaic, the gamma curve and BT.601 video range in the ISP (about
// 5.4 ms, between two of the sensor's frame starts, beside the capture), the 4:2:2 the ISP writes made
// 4:2:0 in the kernel: the MDP writes the luma straight into dst and lays the chroma out, the cores
// average its lines (research/mt8163-isp-pass2.md, "Stage 4"). The cores copy the frame in and do that
// average; the result is the same bytes as the kernel's conversion on the cores.
//
// The picture is the cores' one (I420) with a better demosaic: the same tone to within a code, every
// pixel's own colors where the cores take one per 2x2 cell, so edges keep their color and the noise
// is finer. Any error turns it off for good (logged once), and the stream goes on the cores' I420.

// cronosDevelop is struct cronos_develop: fixed-size fields, the same for 32- and 64-bit callers.
type cronosDevelop struct {
	raw, dst         uint64
	rawSize, dstSize uint32
	black            int32     // added to every sample, on the ISP's 12-bit scale (a 10-bit sample x 4)
	gain             [4]uint32 // red, green (red rows), green (blue rows), blue, in 1/512
	curve            [145]uint16
	_                uint16
	flags            uint32
	hwUs             uint32 // back: the ISP's time
}

// CRONOS_IOC_DEVELOP is _IOWR('V', BASE_VIDIOC_PRIVATE + 0, struct cronos_develop).
var cronosIocDevelop = ioc(3, 'V', 192, unsafe.Sizeof(cronosDevelop{}))

// ispCurveWhite is the curve's output for white: the ISP's luma is 10 bits, a quarter of a code each,
// and its BT.601 takes 1020 as the cores' 255.
const ispCurveWhite = 1020

// ispKnot is the 12-bit input of the ISP's gamma table at entry i: 64 entries 8 apart, 32 16 apart, 32
// 32 apart and 16 128 apart; i = 144 is the end of the range.
func ispKnot(i int) int {
	switch {
	case i < 64:
		return 8 * i
	case i < 96:
		return 512 + 16*(i-64)
	case i < 128:
		return 1024 + 32*(i-96)
	}
	return 2048 + 128*(i-128)
}

// ispDevelopTone is a frame's tone as the ISP applies it. The cores level a channel as
// clamp((2x - black) * gain / (white - black)) on the 11-bit scale, x the 10-bit sample, then raise it
// to the gamma; on the ISP's 12-bit scale X = 4x that is (X - 2 black) * gain * 2048/(white - black)
// over 4096: the offset is -2 black, the gains carry the stretch, and the curve is the gamma alone.
func ispDevelopTone(t tone, d *cronosDevelop) {
	d.black = int32(max(-2*t.black, -4096))
	span := float64(max(t.white-t.black, 1))
	for c, g := range [4]float64{t.gainR, 1, 1, t.gainB} {
		d.gain[c] = uint32(min(math.Round(g*2048/span*512), 256*512))
	}
	for i := range d.curve {
		f := float64(ispKnot(i)) / 4096
		d.curve[i] = uint16(math.Round(math.Pow(f, t.gamma) * ispCurveWhite))
	}
}

// ispDev is the open node the developments go through, and whether they still may.
var ispDev struct {
	sync.Mutex
	fd   int
	off  bool // failed once, or not this board: the cores develop from now on
	open bool
}

// ispDevelops is whether frames go to the ISP: the 2nd gen Show 5 on its mainline kernel, until the
// first failure. TECHO5_CAMERA_ISP=0 keeps them on the cores.
func ispDevelops() bool {
	return boardSensor() == sensorOV02B10 && os.Getenv("TECHO5_CAMERA_ISP") != "0"
}

// ISPDevelops is whether the stream's frames go to the ISP: this board, and no failure yet (the kernel
// says whether it has the develop only when first asked).
func ISPDevelops() bool {
	ispDev.Lock()
	defer ispDev.Unlock()
	return ispDevelops() && !ispDev.off
}

// ISPI420 develops the frame on the ISP into dst as I420, as I420 does on the cores (same size, same
// range, same tone); ok is false where it cannot (another board, an old kernel, an error), and the
// caller develops on the cores. dst holds w*h*3/2 bytes.
func (f *Frame) ISPI420(dst []byte) (w, h int, ok bool) {
	f.mu.Lock()
	raw, g, full := f.raw, geometryOf(f), f.full != nil
	var t tone
	if raw != nil {
		t = f.level()
	}
	f.mu.Unlock()
	if raw == nil || full || g.w != 1600 || g.h != 1200 || len(dst) < g.w*g.h*3/2 || len(raw) < g.bpl*g.h || !ispDevelops() {
		return 0, 0, false
	}
	if _, err := ispDevelop(raw, dst, t); err != nil {
		return 0, 0, false
	}
	return g.w, g.h, true
}

// ispDevelop hands one frame to the ISP and returns the ISP's time in microseconds.
func ispDevelop(raw, dst []byte, t tone) (int, error) {
	ispDev.Lock()
	defer ispDev.Unlock()
	if ispDev.off {
		return 0, errors.New("camera: the ISP develop is off")
	}
	fail := func(err error) (int, error) {
		ispDev.off = true
		if ispDev.open {
			syscall.Close(ispDev.fd)
			ispDev.open = false
		}
		slog.Warn("camera: the ISP develop failed; developing on the cores from now on", "err", err)
		return 0, err
	}
	if !ispDev.open {
		path := mainlineCameraPath()
		if path == "" {
			return fail(errors.New("no mainline camera"))
		}
		fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CLOEXEC, 0)
		if err != nil {
			return fail(err)
		}
		ispDev.fd, ispDev.open = fd, true
	}
	d := heapNew[cronosDevelop]()
	d.raw, d.rawSize = uint64(uintptr(unsafe.Pointer(&raw[0]))), uint32(len(raw))
	d.dst, d.dstSize = uint64(uintptr(unsafe.Pointer(&dst[0]))), uint32(len(dst))
	ispDevelopTone(t, d)
	err := ioctl(ispDev.fd, cronosIocDevelop, unsafe.Pointer(d))
	runtime.KeepAlive(raw)
	runtime.KeepAlive(dst)
	if err != nil {
		if err == syscall.ENOTTY || err == syscall.EINVAL {
			return fail(fmt.Errorf("the kernel has no ISP develop: %w", err))
		}
		return fail(err)
	}
	return int(d.hwUs), nil
}
