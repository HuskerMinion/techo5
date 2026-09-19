//go:build !dot && !spot

package camera

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func mainlineCameraPath() string {
	paths, _ := filepath.Glob("/sys/class/video4linux/video*/name")
	for _, path := range paths {
		name, err := os.ReadFile(path)
		if err == nil && strings.TrimSpace(string(name)) == "cronos-ov02b10" {
			return "/dev/" + filepath.Base(filepath.Dir(path))
		}
	}
	return ""
}

func Available() bool { return mainlineCameraPath() != "" || availableVendor() }

func openV4L2(path string) (*device, error) {
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open camera: %w", err)
	}
	// v4l2_format has a 200-byte union, with four-byte alignment on armv7.
	format := [51]uint32{1, sensorW, sensorH, uint32('p') | uint32('R')<<8 | uint32('A')<<16 | uint32('A')<<24}
	if err := ioctl(fd, ioc(3, 'V', 5, unsafe.Sizeof(format)), unsafe.Pointer(&format)); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("camera format: %w", err)
	}
	if format[1] != sensorW || format[2] != sensorH || format[6] != frameBytes {
		syscall.Close(fd)
		return nil, fmt.Errorf("unexpected camera format %dx%d, %d bytes", format[1], format[2], format[6])
	}
	d := &device{isp: fd, sens: -1, ion: -1, v4l2: true, shutter: 600, gain: 128, settle: aeDelay}
	d.setFeature(featShutter, uint64(d.shutter))
	d.setFeature(featGain, uint64(d.gain))
	return d, nil
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
	if err := ioctl(d.isp, ioc(3, 'V', 28, 8), unsafe.Pointer(&arg)); err != nil {
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
