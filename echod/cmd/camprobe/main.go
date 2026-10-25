//go:build linux

// Command camprobe is the bench check for the Echo Show 8 2nd gen's camera on the mainline kernel:
// OV13855 -> seninf -> CAMSV, all V4L2. It finds the three subdevices and the CAMSV video node by
// name in sysfs, sets one format along the chain, streams a few frames and writes the raw 8-bit
// Bayer frames to a file, with how long each took to arrive.
//
//	camprobe [-w 2112 -h 1568] [-n 10] [-o /tmp/cam.raw] [-pattern 1] [-exposure lines] [-gain 128]
//
// Built for arm64 as well as arm: the kernel runs either, and arm64 uses the native struct layouts.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	bufTypeCaptureMplane = 9 // V4L2_BUF_TYPE_VIDEO_CAPTURE_MPLANE
	memoryMmap           = 1
	fieldNone            = 1
	subdevFormatActive   = 1

	mbusSBGGR10 = 0x3007
	pixSBGGR8   = 0x31384142 // 'BA81'
	pixSBGGR10P = 0x41414270 // 'pBAA': MediaTek's LSB-first packing with mtk_camsv.out_mode=1

	cidExposure    = 0x00980911
	cidAnalogGain  = 0x009e0903
	cidTestPattern = 0x009f0903
	cidVBlank      = 0x009e0901
)

func ioc(dir, nr, size uintptr) uintptr { return dir<<30 | size<<16 | 'V'<<8 | nr }

const (
	iocW  = 1
	iocR  = 2
	iocWR = 3
)

var ptrSize = unsafe.Sizeof(uintptr(0))

// v4l2_buffer and v4l2_plane: the only structs here whose layout depends on the ABI. 64-bit:
// timeval 16 bytes at 24, the m union 8 bytes at 64, 88 in all; plane 64. 32-bit (the compat
// "time32" layout, timeval 8 bytes): m at 52, 68 in all; plane 60.
type bufLayout struct{ size, seq, mOff, length, planeSize, planeMOff uintptr }

func layout() bufLayout {
	if ptrSize == 8 {
		return bufLayout{size: 88, seq: 56, mOff: 64, length: 72, planeSize: 64, planeMOff: 8}
	}
	return bufLayout{size: 68, seq: 44, mOff: 52, length: 56, planeSize: 60, planeMOff: 8}
}

// v4l2_format: the union follows the type after padding to pointer size.
func fmtSize() uintptr { return ptrSize + 200 }

func ioctl(fd int, req uintptr, arg unsafe.Pointer) error {
	_, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(arg))
	if e != 0 {
		return e
	}
	return nil
}

func le32(b []byte, off int) uint32 {
	return uint32(b[off]) | uint32(b[off+1])<<8 | uint32(b[off+2])<<16 | uint32(b[off+3])<<24
}

func put32(b []byte, off int, v uint32) {
	b[off], b[off+1], b[off+2], b[off+3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}

// findNode returns /dev/<node> for the first video4linux node whose name matches.
func findNode(match func(string) bool) (string, string, error) {
	dirs, _ := filepath.Glob("/sys/class/video4linux/*")
	for _, d := range dirs {
		n, err := os.ReadFile(d + "/name")
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(n))
		if match(name) {
			return "/dev/" + filepath.Base(d), name, nil
		}
	}
	return "", "", errors.New("not found")
}

func subdevFmt(fd int, pad, w, h, code uint32) (uint32, uint32, uint32, error) {
	var b [88]byte
	put32(b[:], 0, subdevFormatActive)
	put32(b[:], 4, pad)
	put32(b[:], 8, w)
	put32(b[:], 12, h)
	put32(b[:], 16, code)
	put32(b[:], 20, fieldNone)
	if err := ioctl(fd, ioc(iocWR, 5, 88), unsafe.Pointer(&b[0])); err != nil {
		return 0, 0, 0, err
	}
	return le32(b[:], 8), le32(b[:], 12), le32(b[:], 16), nil
}

func setCtrl(fd int, id uint32, v int32) error {
	var b [8]byte
	put32(b[:], 0, id)
	put32(b[:], 4, uint32(v))
	return ioctl(fd, ioc(iocWR, 28, 8), unsafe.Pointer(&b[0]))
}

func open(path string) int {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open %s: %v\n", path, err)
		os.Exit(1)
	}
	return fd
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

func main() {
	w := flag.Uint("w", 2112, "width")
	h := flag.Uint("h", 1568, "height")
	n := flag.Int("n", 10, "frames to keep")
	out := flag.String("o", "/tmp/cam.raw", "raw output (frames back to back)")
	pattern := flag.Int("pattern", 0, "sensor test pattern (0 off, 1..4 color bars)")
	exposure := flag.Int("exposure", 0, "exposure in lines (0: the driver's)")
	gain := flag.Int("gain", 0, "analog gain, 128 = 1x (0: the driver's)")
	vblank := flag.Int("vblank", 0, "vertical blanking in lines (0: the driver's)")
	bits := flag.Int("bits", 8, "8, or 10 (packed; needs mtk_camsv.out_mode=1)")
	copyTime := flag.Bool("copytime", false, "time a copy of each frame out of its DMA buffer")
	cached := flag.Bool("cached", true, "ask for cached buffers (V4L2_MEMORY_FLAG_NON_COHERENT)")
	flag.Parse()

	sensor, sname, err := findNode(func(s string) bool { return strings.HasPrefix(s, "ov13855") })
	if err != nil {
		die("no ov13855 subdevice: did the sensor probe? (dmesg | grep -i ov13855)")
	}
	seninf, _, err := findNode(func(s string) bool { return strings.HasSuffix(s, ".seninf") })
	if err != nil {
		die("no seninf subdevice")
	}
	camsv, cname, err := findNode(func(s string) bool { return strings.HasSuffix(s, ".camsv") })
	if err != nil {
		die("no camsv subdevice")
	}
	video, _, err := findNode(func(s string) bool { return s == cname+" video stream" })
	if err != nil {
		die("no %q video node", cname+" video stream")
	}
	fmt.Printf("sensor %s (%s), seninf %s, camsv %s, video %s\n", sensor, sname, seninf, camsv, video)

	sfd := open(sensor)
	if *pattern > 0 {
		if err := setCtrl(sfd, cidTestPattern, int32(*pattern)); err != nil {
			die("test pattern: %v", err)
		}
	}
	gw, gh, code, err := subdevFmt(sfd, 0, uint32(*w), uint32(*h), mbusSBGGR10)
	if err != nil {
		die("sensor format: %v", err)
	}
	fmt.Printf("sensor: %dx%d code %#x\n", gw, gh, code)
	if *vblank > 0 {
		if err := setCtrl(sfd, cidVBlank, int32(*vblank)); err != nil {
			die("vblank: %v", err)
		}
	}
	if *exposure > 0 {
		if err := setCtrl(sfd, cidExposure, int32(*exposure)); err != nil {
			die("exposure: %v", err)
		}
	}
	if *gain > 0 {
		if err := setCtrl(sfd, cidAnalogGain, int32(*gain)); err != nil {
			die("gain: %v", err)
		}
	}

	nfd := open(seninf)
	if _, _, _, err := subdevFmt(nfd, 0, gw, gh, code); err != nil {
		die("seninf sink format: %v", err)
	}
	if ow, oh, oc, err := subdevFmt(nfd, 4, gw, gh, code); err != nil {
		fmt.Printf("seninf source format: %v (left to the routing)\n", err)
	} else {
		fmt.Printf("seninf source: %dx%d code %#x\n", ow, oh, oc)
	}
	cfd := open(camsv)
	if _, _, _, err := subdevFmt(cfd, 0, gw, gh, code); err != nil {
		die("camsv format: %v", err)
	}

	vfd := open(video)
	fsz := fmtSize()
	f := make([]byte, fsz)
	u := int(ptrSize)
	put32(f, 0, bufTypeCaptureMplane)
	put32(f, u+0, gw)
	put32(f, u+4, gh)
	pix := uint32(pixSBGGR8)
	if *bits == 10 {
		pix = pixSBGGR10P
	}
	put32(f, u+8, pix)
	put32(f, u+12, fieldNone)
	if err := ioctl(vfd, ioc(iocWR, 5, fsz), unsafe.Pointer(&f[0])); err != nil {
		die("video format: %v", err)
	}
	vw, vh := le32(f, u), le32(f, u+4)
	pix = le32(f, u+8)
	sizeimage, bpl := le32(f, u+20), le32(f, u+24)
	fmt.Printf("video: %dx%d %q, %d planes, line %d bytes, frame %d bytes\n",
		vw, vh, string([]byte{byte(pix), byte(pix >> 8), byte(pix >> 16), byte(pix >> 24)}),
		f[u+180], bpl, sizeimage)

	const nbuf = 4
	var req [20]byte
	put32(req[:], 0, nbuf)
	put32(req[:], 4, bufTypeCaptureMplane)
	put32(req[:], 8, memoryMmap)
	if *cached {
		req[16] = 1 // V4L2_MEMORY_FLAG_NON_COHERENT
	}
	if err := ioctl(vfd, ioc(iocWR, 8, 20), unsafe.Pointer(&req[0])); err != nil {
		die("reqbufs: %v", err)
	}
	L := layout()
	plane := make([]byte, L.planeSize)
	buf := make([]byte, L.size)
	setBuf := func(i uint32) {
		clear(buf)
		clear(plane)
		put32(buf, 0, i)
		put32(buf, 4, bufTypeCaptureMplane)
		put32(buf, int(L.seq+4), memoryMmap) // memory follows sequence
		p := uintptr(unsafe.Pointer(&plane[0]))
		if ptrSize == 8 {
			put32(buf, int(L.mOff), uint32(p))
			put32(buf, int(L.mOff+4), uint32(uint64(p)>>32))
		} else {
			put32(buf, int(L.mOff), uint32(p))
		}
		put32(buf, int(L.length), 1) // planes
	}
	maps := make([][]byte, le32(req[:], 0))
	for i := range maps {
		setBuf(uint32(i))
		if err := ioctl(vfd, ioc(iocWR, 9, L.size), unsafe.Pointer(&buf[0])); err != nil {
			die("querybuf %d: %v", i, err)
		}
		length := le32(plane, 4)
		offset := le32(plane, int(L.planeMOff))
		m, err := unix.Mmap(vfd, int64(offset), int(length), unix.PROT_READ, unix.MAP_SHARED)
		if err != nil {
			die("mmap %d: %v", i, err)
		}
		maps[i] = m
		if err := ioctl(vfd, ioc(iocWR, 15, L.size), unsafe.Pointer(&buf[0])); err != nil {
			die("qbuf %d: %v", i, err)
		}
	}

	typ := int32(bufTypeCaptureMplane)
	if err := ioctl(vfd, ioc(iocW, 18, 4), unsafe.Pointer(&typ)); err != nil {
		die("streamon: %v (dmesg has the reason)", err)
	}
	defer ioctl(vfd, ioc(iocW, 19, 4), unsafe.Pointer(&typ))

	of, err := os.Create(*out)
	if err != nil {
		die("%v", err)
	}
	defer of.Close()
	start, last := time.Now(), time.Now()
	for got := 0; got < *n+2; got++ { // the first two are dropped: exposure settles
		pfd := []unix.PollFd{{Fd: int32(vfd), Events: unix.POLLIN}}
		k, err := unix.Poll(pfd, 3000)
		if errors.Is(err, unix.EINTR) { // the Go runtime's preemption signal
			got--
			continue
		}
		if err != nil || k == 0 {
			die("no frame within 3 s after %d (poll %v); dmesg and /sys/kernel/debug/*camsv*/ may say why", got, err)
		}
		setBuf(0)
		if err := ioctl(vfd, ioc(iocWR, 17, L.size), unsafe.Pointer(&buf[0])); err != nil {
			die("dqbuf: %v", err)
		}
		i := le32(buf, 0)
		used := le32(plane, 0)
		if *copyTime && int(i) < len(maps) && int(used) <= len(maps[i]) {
			t0 := time.Now()
			kept := make([]byte, used)
			copy(kept, maps[i][:used])
			fmt.Printf("copy of %d bytes: %.2f ms\n", used, float64(time.Since(t0).Microseconds())/1000)
		}
		now := time.Now()
		fmt.Printf("frame %d: buffer %d, seq %d, %d bytes, %.1f ms\n", got, i, le32(buf, int(L.seq)), used,
			float64(now.Sub(last).Microseconds())/1000)
		last = now
		if got >= 2 && int(i) < len(maps) && int(used) <= len(maps[i]) {
			of.Write(maps[i][:used])
		}
		if err := ioctl(vfd, ioc(iocWR, 15, L.size), unsafe.Pointer(&buf[0])); err != nil {
			die("requeue: %v", err)
		}
	}
	el := time.Since(start).Seconds()
	fmt.Printf("%d frames in %.2f s (%.1f a second), written to %s\n", *n+2, el, float64(*n+2)/el, *out)
}
