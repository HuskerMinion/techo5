//go:build !dot && !spot

package camera

import (
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

// mainlineCameraPath finds the camera the mainline kernels expose: cronos-camera on the Show 5 2nd
// gen, athena-camera on the Show 8 2nd gen. Both hand over packed 10-bit frames the size of sensor.
func mainlineCameraPath() string {
	paths, _ := filepath.Glob("/sys/class/video4linux/video*/name")
	for _, path := range paths {
		name, err := os.ReadFile(path)
		if n := strings.TrimSpace(string(name)); err == nil && (n == "cronos-ov02b10" || n == "athena-ov13855") {
			return "/dev/" + filepath.Base(filepath.Dir(path))
		}
	}
	return ""
}

// v4l2PixFormat is struct v4l2_pix_format: twelve 32-bit fields, 48 bytes on every ABI.
type v4l2PixFormat struct {
	width, height, pixelformat, field, bytesperline, sizeimage uint32
	colorspace, priv, flags, encoding, quantization, xferFunc  uint32
}

// v4l2Format is struct v4l2_format: a 32-bit buffer type, then a 200-byte union. One member of the
// union, struct v4l2_window, holds pointers, so the union takes pointer alignment: on armv7 it starts
// at 4 and the struct is 204 bytes, on arm64 it starts at 8 and the struct is 208. The size is in
// VIDIOC_S_FMT's number, so a layout built for one ABI is refused outright (ENOTTY) on the other. The
// zero-length uintptr array gives the union that alignment without taking any room.
type v4l2Format struct {
	typ   uint32
	union struct {
		_   [0]uintptr
		pix v4l2PixFormat
		_   [200 - unsafe.Sizeof(v4l2PixFormat{})]byte
	}
}

const v4l2BufTypeVideoCapture = 1

var (
	// VIDIOC_S_FMT is _IOWR('V', 5, struct v4l2_format).
	vidiocSFmt = ioc(3, 'V', 5, unsafe.Sizeof(v4l2Format{}))
	// VIDIOC_S_CTRL is _IOWR('V', 28, struct v4l2_control): two 32-bit fields on every ABI.
	vidiocSCtrl = ioc(3, 'V', 28, unsafe.Sizeof([2]uint32{}))
)

func openCapture(path string) (*device, error) {
	fd, err := openSized(path, streamed())
	if err != nil {
		return nil, err
	}
	d := &device{isp: fd, sens: -1, ion: -1, v4l2: true, shutter: 600, gain: 128, settle: aeDelay}
	d.setFeature(featShutter, uint64(d.shutter))
	d.setFeature(featGain, uint64(d.gain))
	return d, nil
}

// openSized opens the camera with the format of this size, which on the 2nd gen Show 8 picks the
// sensor's mode: the binned one that streams, or the whole array for a still.
func openSized(path string, g geometry) (int, error) {
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("open camera: %w", err)
	}
	format := v4l2Format{typ: v4l2BufTypeVideoCapture}
	format.union.pix.width, format.union.pix.height = uint32(g.w), uint32(g.h)
	format.union.pix.pixelformat = uint32('p') | uint32('R')<<8 | uint32('A')<<16 | uint32('A')<<24
	if err := ioctl(fd, vidiocSFmt, unsafe.Pointer(&format)); err != nil {
		syscall.Close(fd)
		return -1, fmt.Errorf("camera format: %w", err)
	}
	if format.union.pix.width != uint32(g.w) || format.union.pix.height != uint32(g.h) || format.union.pix.sizeimage != uint32(g.bpl*g.h) {
		syscall.Close(fd)
		return -1, fmt.Errorf("unexpected camera format %dx%d, %d bytes", format.union.pix.width, format.union.pix.height, format.union.pix.sizeimage)
	}
	return fd, nil
}

// fullGeometry is the whole array, where the sensor has a mode bigger than the stream's.
func fullGeometry() (geometry, bool) {
	if sensor.fullW == 0 {
		return geometry{}, false
	}
	return geometry{sensor.fullW, sensor.fullH, sensor.fullW * 10 / 8}, true
}

// stillFrames is how many frames a still reads before the one it keeps: the driver drops its first,
// and the exposure set before the start is in force from the next.
const stillFrames = 2

// captureFull takes one frame from the whole array in the stream's place and puts the stream back.
// The exposure carries over in time, not in lines: a line is longer in that mode (sensor.fullLine).
// The format can change only with the device closed, so the stream's file goes, and comes back.
func (d *device) captureFull() ([]byte, error) {
	g, ok := fullGeometry()
	if !ok {
		return nil, fmt.Errorf("camera: no mode bigger than the stream")
	}
	path := mainlineCameraPath()
	syscall.Close(d.isp)
	defer func() {
		// Back to the stream's mode at the exposure it had, whatever the still did.
		fd, err := openSized(path, streamed())
		if err != nil {
			slog.Error("camera: back to the stream after a still", "err", err)
			d.isp = -1
			return
		}
		d.isp = fd
		d.setFeature(featShutter, uint64(d.shutter))
		d.setFeature(featGain, uint64(d.gain))
	}()
	fd, err := openSized(path, g)
	if err != nil {
		return nil, err
	}
	defer syscall.Close(fd)
	still := &device{isp: fd, sens: -1, ion: -1, v4l2: true}
	still.setFeature(featShutter, uint64(max(sensor.minShut, int(float64(d.shutter)/sensor.fullLine))))
	still.setFeature(featGain, uint64(d.gain))
	buf := make([]byte, g.bpl*g.h)
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	deadline := time.Now().Add(5 * time.Second)
	for read := 0; read <= stillFrames; {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("camera: no still from the whole array in time")
		}
		n, err := syscall.Read(fd, buf)
		switch {
		case err == syscall.EINTR:
		case err == syscall.EAGAIN:
			unix.Poll(poll, 100)
		case err != nil || n != len(buf):
			return nil, fmt.Errorf("camera: still read %d bytes: %v", n, err)
		default:
			read++
		}
	}
	return buf, nil
}

func (d *device) v4l2Feature(id uint32, value uint64) {
	var control uint32
	switch id {
	case featShutter:
		control = 0x00980911 // V4L2_CID_EXPOSURE
	case featGain:
		control = 0x009e0903 // V4L2_CID_ANALOGUE_GAIN
	default:
		return
	}
	arg := [2]uint32{control, uint32(value)}
	if err := ioctl(d.isp, vidiocSCtrl, unsafe.Pointer(&arg)); err != nil {
		slog.Warn("camera control", "id", control, "err", err)
	}
}

func (d *device) streamV4L2(stop chan struct{}, frame func([]byte)) {
	buf := make([]byte, frameBytes)
	poll := []unix.PollFd{{Fd: int32(d.isp), Events: unix.POLLIN}}
	for {
		select {
		case <-stop:
			return
		case req := <-Get().stills:
			raw, err := d.captureFull()
			req.done <- stillResult{raw, err}
			if d.isp < 0 {
				// The stream could not come back; the owner restarts it on the next use.
				<-stop
				return
			}
			poll[0].Fd = int32(d.isp)
			continue
		default:
		}
		n, err := syscall.Read(d.isp, buf)
		if err == syscall.EINTR {
			continue
		}
		if err == syscall.EAGAIN {
			unix.Poll(poll, 100)
			continue
		}
		if err != nil || n != len(buf) {
			slog.Error("camera capture", "bytes", n, "err", err)
			// The owner coordinates shutdown with its privacy watcher.
			<-stop
			return
		}
		frame(buf)
	}
}
