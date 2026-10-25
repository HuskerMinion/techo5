//go:build !dot && !spot && !panel

package camera

// The Echo Show 8 2nd gen (athena) on its mainline kernel: the OV13855 behind MediaTek's sensor
// interface (seninf) and CAMSV, all plain V4L2 (tools/linux/athena/kernel/patches/patch-camera.py).
// CAMSV writes the frame as MediaTek packs 10-bit Bayer, least significant bits first, which is the
// Show 5's IMGO layout too: everything from the frame on (metering, levels, render, Full, the luma
// grid) is shared, and this file only opens the chain, streams it and sets the sensor's exposure.
//
// The chain is found by name in sysfs: the sensor subdevice "ov13855 <bus>-0036", the seninf
// "<addr>.seninf", the CAMSV subdevice "<addr>.camsv" and its video node "<addr>.camsv video stream".
// The links are fixed in the device tree; only the formats are set, the same size all along.

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	v4l2BufCaptureMplane = 9 // V4L2_BUF_TYPE_VIDEO_CAPTURE_MPLANE
	v4l2MemoryMmap       = 1
	v4l2FieldNone        = 1
	v4l2FormatActive     = 1

	v4l2MemoryFlagNonCoherent = 1 // V4L2_MEMORY_FLAG_NON_COHERENT, in v4l2_requestbuffers.flags

	mbusSBGGR10 = 0x3007     // MEDIA_BUS_FMT_SBGGR10_1X10
	pixSBGGR10P = 0x41414270 // 'pBAA': what CAMSV advertises for its packed 10-bit output

	cidExposure   = 0x00980911 // V4L2_CID_EXPOSURE, in lines
	cidAnalogGain = 0x009e0903 // V4L2_CID_ANALOGUE_GAIN, 128 = 1x on the OV13855

	v4l2Bufs = 4
)

// v4l2Sysfs is where the nodes are looked up; a test points it elsewhere.
var v4l2Sysfs = "/sys/class/video4linux"

func vioc(dir, nr, size uintptr) uintptr { return dir<<30 | size<<16 | 'V'<<8 | nr }

var wordSize = unsafe.Sizeof(uintptr(0))

// v4l2_buffer and v4l2_plane are the only structures here whose layout depends on the ABI. 32-bit
// (the daemon on the unit, through the kernel's compat layer, with the time32 layout): the m union
// at 52, 68 bytes; a plane 60. 64-bit (tests on the build host): the timeval is 16 bytes, m at 64,
// 88 bytes; a plane 64.
type bufLayout struct{ size, seq, mOff, length, planeSize uintptr }

func v4l2Layout() bufLayout {
	if wordSize == 8 {
		return bufLayout{size: 88, seq: 56, mOff: 64, length: 72, planeSize: 64}
	}
	return bufLayout{size: 68, seq: 44, mOff: 52, length: 56, planeSize: 60}
}

func le32(b []byte, off uintptr) uint32 {
	return uint32(b[off]) | uint32(b[off+1])<<8 | uint32(b[off+2])<<16 | uint32(b[off+3])<<24
}

func put32(b []byte, off uintptr, v uint32) {
	b[off], b[off+1], b[off+2], b[off+3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}

// v4l2Node is the /dev path of the video4linux node whose name matches.
func v4l2Node(match func(string) bool) (string, string) {
	dirs, _ := filepath.Glob(v4l2Sysfs + "/*")
	for _, d := range dirs {
		n, err := os.ReadFile(d + "/name")
		if err != nil {
			continue
		}
		if name := strings.TrimSpace(string(n)); match(name) {
			return "/dev/" + filepath.Base(d), name
		}
	}
	return "", ""
}

type v4l2Chain struct{ sensor, seninf, camsv, video string }

func findChain() (v4l2Chain, error) {
	var c v4l2Chain
	var camsvName string
	c.sensor, _ = v4l2Node(func(s string) bool { return strings.HasPrefix(s, "ov13855 ") })
	c.seninf, _ = v4l2Node(func(s string) bool { return strings.HasSuffix(s, ".seninf") })
	c.camsv, camsvName = v4l2Node(func(s string) bool { return strings.HasSuffix(s, ".camsv") })
	if camsvName != "" {
		c.video, _ = v4l2Node(func(s string) bool { return s == camsvName+" video stream" })
	}
	switch {
	case c.sensor == "":
		return c, errors.New("no OV13855 (the sensor did not answer when the kernel started: was the camera muted?)")
	case c.seninf == "" || c.camsv == "" || c.video == "":
		return c, errors.New("the camera's receive chain is missing")
	}
	return c, nil
}

// v4l2Available is whether the whole chain is there.
func v4l2Available() bool {
	_, err := findChain()
	return err == nil
}

// v4l2Cam is one run of the chain: its nodes open, the buffers mapped and queued.
type v4l2Cam struct {
	sensor, seninf, camsv, video int
	maps                         [][]byte
	lay                          bufLayout
	buf                          *[88]byte // one v4l2_buffer, reused; the larger layout fits both
	plane                        *[64]byte
}

func subdevFormat(fd int, pad, w, h, code uint32) error {
	var b [88]byte // v4l2_subdev_format: no pointers, the same on every ABI
	put32(b[:], 0, v4l2FormatActive)
	put32(b[:], 4, pad)
	put32(b[:], 8, w)
	put32(b[:], 12, h)
	put32(b[:], 16, code)
	put32(b[:], 20, v4l2FieldNone)
	if err := ioctl(fd, vioc(3, 5, 88), unsafe.Pointer(&b[0])); err != nil {
		return err
	}
	if le32(b[:], 8) != w || le32(b[:], 12) != h || le32(b[:], 16) != code {
		return fmt.Errorf("pad %d took %dx%d %#x", pad, le32(b[:], 8), le32(b[:], 12), le32(b[:], 16))
	}
	return nil
}

func setCtrl(fd int, id uint32, v int32) error {
	var b [8]byte
	put32(b[:], 0, id)
	put32(b[:], 4, uint32(v))
	return ioctl(fd, vioc(3, 28, 8), unsafe.Pointer(&b[0]))
}

func openV4L2() (*v4l2Cam, error) {
	chain, err := findChain()
	if err != nil {
		return nil, err
	}
	v := &v4l2Cam{sensor: -1, seninf: -1, camsv: -1, video: -1, lay: v4l2Layout(),
		buf: heapNew[[88]byte](), plane: heapNew[[64]byte]()}
	for _, o := range []struct {
		fd   *int
		path string
	}{{&v.sensor, chain.sensor}, {&v.seninf, chain.seninf}, {&v.camsv, chain.camsv}, {&v.video, chain.video}} {
		if *o.fd, err = syscall.Open(o.path, syscall.O_RDWR|syscall.O_CLOEXEC, 0); err != nil {
			v.close()
			return nil, fmt.Errorf("open %s: %w", o.path, err)
		}
	}
	w, h := uint32(sensorW), uint32(sensorH)
	for _, s := range []struct {
		fd   int
		pad  uint32
		what string
	}{{v.sensor, 0, "sensor"}, {v.seninf, 0, "seninf"}, {v.camsv, 0, "camsv"}} {
		if err := subdevFormat(s.fd, s.pad, w, h, mbusSBGGR10); err != nil {
			v.close()
			return nil, fmt.Errorf("camera %s format: %w", s.what, err)
		}
	}

	f := make([]byte, wordSize+200) // v4l2_format: the union follows the type, aligned to a word
	u := wordSize
	put32(f, 0, v4l2BufCaptureMplane)
	put32(f, u, w)
	put32(f, u+4, h)
	put32(f, u+8, pixSBGGR10P)
	put32(f, u+12, v4l2FieldNone)
	if err := ioctl(v.video, vioc(3, 5, wordSize+200), unsafe.Pointer(&f[0])); err != nil {
		v.close()
		return nil, fmt.Errorf("camera video format: %w", err)
	}
	if pix, bpl := le32(f, u+8), le32(f, u+24); pix != pixSBGGR10P || int(bpl) != bytesPerLine {
		v.close()
		return nil, fmt.Errorf("camera video format: %#x with %d bytes a line, want packed 10-bit at %d "+
			"(the kernel needs mtk_camsv.out_mode=1)", pix, bpl, bytesPerLine)
	}

	var req [20]byte
	put32(req[:], 0, v4l2Bufs)
	put32(req[:], 4, v4l2BufCaptureMplane)
	put32(req[:], 8, v4l2MemoryMmap)
	// Cached buffers: the MT8183 is not DMA-coherent, and reading a frame out of the default
	// uncached mapping cost 10-45 ms (4 MB). The kernel then syncs the cache at each dequeue.
	// A kernel without the hint for this queue ignores the flag and maps it uncached as before.
	req[16] = v4l2MemoryFlagNonCoherent
	if err := ioctl(v.video, vioc(3, 8, 20), unsafe.Pointer(&req[0])); err != nil {
		v.close()
		return nil, fmt.Errorf("camera buffers: %w", err)
	}
	v.maps = make([][]byte, le32(req[:], 0))
	for i := range v.maps {
		v.setBuf(uint32(i))
		if err := ioctl(v.video, vioc(3, 9, v.lay.size), unsafe.Pointer(&v.buf[0])); err != nil {
			v.close()
			return nil, fmt.Errorf("camera buffer %d: %w", i, err)
		}
		length, offset := le32(v.plane[:], 4), le32(v.plane[:], 8)
		m, err := unix.Mmap(v.video, int64(offset), int(length), unix.PROT_READ, unix.MAP_SHARED)
		if err != nil {
			v.close()
			return nil, fmt.Errorf("camera buffer %d map: %w", i, err)
		}
		v.maps[i] = m
		if err := v.queue(); err != nil {
			v.close()
			return nil, fmt.Errorf("camera buffer %d queue: %w", i, err)
		}
	}
	return v, nil
}

// setBuf readies v.buf for buffer i, its one plane at v.plane.
func (v *v4l2Cam) setBuf(i uint32) {
	*v.buf, *v.plane = [88]byte{}, [64]byte{}
	b := v.buf[:]
	put32(b, 0, i)
	put32(b, 4, v4l2BufCaptureMplane)
	put32(b, v.lay.seq+4, v4l2MemoryMmap) // memory follows sequence
	p := uint64(uintptr(unsafe.Pointer(&v.plane[0])))
	put32(b, v.lay.mOff, uint32(p))
	if wordSize == 8 {
		put32(b, v.lay.mOff+4, uint32(p>>32))
	}
	put32(b, v.lay.length, 1) // planes
}

func (v *v4l2Cam) queue() error {
	return ioctl(v.video, vioc(3, 15, v.lay.size), unsafe.Pointer(&v.buf[0]))
}

// exposure sets the shutter in lines and the gain in the Show 5's 1/64 steps.
func (v *v4l2Cam) exposure(shutter, gain int) {
	if err := setCtrl(v.sensor, cidExposure, int32(shutter)); err != nil {
		slog.Warn("camera: exposure", "lines", shutter, "err", err)
	}
	if err := setCtrl(v.sensor, cidAnalogGain, int32(gain*2)); err != nil {
		slog.Warn("camera: gain", "gain", gain, "err", err)
	}
}

// stream runs until stop closes, calling frame with each frame as it comes off the DMA. The buffer
// goes back to the driver as soon as frame returns, so frame must copy what it keeps.
func (v *v4l2Cam) stream(stop chan struct{}, frame func(bayer []byte)) {
	typ := int32(v4l2BufCaptureMplane)
	// Starting can fail where opening did not: the sensor is only identified when it first streams
	// if it was silent at boot. Keep trying rather than sit as a running camera with no frames;
	// the buffers stay queued through a failed start.
	for tries := 0; ; tries++ {
		err := ioctl(v.video, vioc(1, 18, 4), unsafe.Pointer(&typ))
		if err == nil {
			break
		}
		if tries%15 == 0 {
			slog.Error("camera: stream on", "err", err, "tries", tries+1)
		}
		select {
		case <-stop:
			return
		case <-time.After(2 * time.Second):
		}
	}
	defer ioctl(v.video, vioc(1, 19, 4), unsafe.Pointer(&typ))
	quiet, failed := time.Now(), time.Time{}
	var lost []uint32 // buffers whose requeue failed
	for {
		select {
		case <-stop:
			return
		default:
		}
		// A buffer that would not go back is tried again each round, rather than lost to the
		// stream for good (with all four lost no frame would come).
		for len(lost) > 0 {
			v.setBuf(lost[0])
			if v.queue() != nil {
				break
			}
			lost = lost[1:]
		}
		pfd := []unix.PollFd{{Fd: int32(v.video), Events: unix.POLLIN}}
		if n, err := unix.Poll(pfd, 200); err != nil || n == 0 {
			if err != nil && !errors.Is(err, unix.EINTR) {
				slog.Warn("camera: poll", "err", err)
			}
			if time.Since(quiet) > 2*time.Second {
				slog.Warn("camera: no frames for 2s")
				quiet = time.Now()
			}
			continue
		}
		v.setBuf(0)
		if err := ioctl(v.video, vioc(3, 17, v.lay.size), unsafe.Pointer(&v.buf[0])); err != nil {
			if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
				continue
			}
			// A queue in error (POLLERR) answers every poll at once: wait a little each time
			// rather than spin, and say so every couple of seconds, not every pass.
			if time.Since(failed) > 2*time.Second {
				slog.Warn("camera: dequeue", "err", err)
				failed = time.Now()
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		quiet = time.Now()
		i, used := le32(v.buf[:], 0), le32(v.plane[:], 0)
		if int(i) < len(v.maps) && int(used) >= frameBytes && len(v.maps[i]) >= frameBytes {
			frame(v.maps[i][:frameBytes])
		}
		if err := v.queue(); err != nil {
			slog.Warn("camera: requeue", "err", err)
			lost = append(lost, i)
		}
	}
}

func (v *v4l2Cam) close() {
	for _, m := range v.maps {
		if m != nil {
			unix.Munmap(m)
		}
	}
	v.maps = nil
	if v.video >= 0 {
		var req [20]byte // free the buffers before the node closes
		put32(req[:], 4, v4l2BufCaptureMplane)
		put32(req[:], 8, v4l2MemoryMmap)
		ioctl(v.video, vioc(3, 8, 20), unsafe.Pointer(&req[0]))
	}
	for _, fd := range []*int{&v.video, &v.camsv, &v.seninf, &v.sensor} {
		if *fd >= 0 {
			syscall.Close(*fd)
			*fd = -1
		}
	}
}
