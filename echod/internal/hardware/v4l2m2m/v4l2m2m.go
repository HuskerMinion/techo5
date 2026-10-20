//go:build linux

// Package v4l2m2m drives the V4L2 memory-to-memory devices of the 2nd gen Show 8's kernel, multi-planar
// kind (the ISP's pass 2, the H.264 encoder: custom-kernel/athena): formats, buffers mapped or shared
// as dmabufs, controls, one frame through at a time.
//
// The structures passed to the kernel are declared as Go structs laid out as the C ones are on the
// architecture (pointers and longs are words), as hardware/jpegenc's are, so the request numbers,
// which carry their sizes, are right on arm and arm64 alike.
package v4l2m2m

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Queue types and memory kinds.
const (
	CaptureMplane = 9  // V4L2_BUF_TYPE_VIDEO_CAPTURE_MPLANE
	OutputMplane  = 10 // V4L2_BUF_TYPE_VIDEO_OUTPUT_MPLANE

	MemoryMmap   = 1
	MemoryDMABuf = 4

	flagKeyframe = 0x8  // V4L2_BUF_FLAG_KEYFRAME
	flagError    = 0x40 // V4L2_BUF_FLAG_ERROR
)

// Fourcc is a pixel format's four characters.
func Fourcc(s string) uint32 {
	return uint32(s[0]) | uint32(s[1])<<8 | uint32(s[2])<<16 | uint32(s[3])<<24
}

type format struct {
	typ   uint32
	union struct {
		_   [0]uintptr
		pix pixFormatMplane
		_   [200 - unsafe.Sizeof(pixFormatMplane{})]byte
	}
}

type pixFormatMplane struct {
	width, height, pixelformat, field, colorspace uint32
	planes                                        [8]planePixFormat
	numPlanes, flags, ycbcr, quantization, xfer   uint8
	_                                             [7]uint8
}

type planePixFormat struct {
	sizeimage, bytesperline uint32
	_                       [6]uint16
}

type requestBuffers struct {
	count, typ, memory, capabilities uint32
	flags                            uint8
	_                                [3]uint8
}

type plane struct {
	bytesused, length uint32
	m                 uintptr // offset (mmap), or the dmabuf's fd
	dataOffset        uint32
	_                 [11]uint32
}

type buffer struct {
	index, typ, bytesused, flags, field uint32
	timestamp                           unix.Timeval
	timecode                            [16]byte
	sequence, memory                    uint32
	m                                   uintptr // the planes
	length, reserved2                   uint32
	requestFD                           int32
}

type exportBuffer struct {
	typ, index, plane, flags uint32
	fd                       int32
	_                        [11]uint32
}

type streamParm struct {
	typ   uint32
	union struct {
		capability, mode, num, den uint32
		_                          [200 - 16]byte
	}
}

type control struct{ id, value uint32 }

// extControl is v4l2_ext_control, which is packed: its value (or pointer) at offset 12, 8 bytes on
// every architecture.
type extControl struct {
	id, size, reserved uint32
	value              [8]byte
}

type extControls struct {
	which, count, errorIdx uint32
	requestFD              int32
	reserved               uint32
	controls               uintptr
}

func ioc(dir, nr, size uintptr) uintptr { return dir<<30 | size<<16 | 'V'<<8 | nr }

var (
	vidiocGFmt      = ioc(3, 4, unsafe.Sizeof(format{}))
	vidiocSFmt      = ioc(3, 5, unsafe.Sizeof(format{}))
	vidiocReqbufs   = ioc(3, 8, unsafe.Sizeof(requestBuffers{}))
	vidiocQuerybuf  = ioc(3, 9, unsafe.Sizeof(buffer{}))
	vidiocQbuf      = ioc(3, 15, unsafe.Sizeof(buffer{}))
	vidiocExpbuf    = ioc(3, 16, unsafe.Sizeof(exportBuffer{}))
	vidiocDqbuf     = ioc(3, 17, unsafe.Sizeof(buffer{}))
	vidiocStreamon  = ioc(1, 18, unsafe.Sizeof(int32(0)))
	vidiocStreamoff = ioc(1, 19, unsafe.Sizeof(int32(0)))
	vidiocSParm     = ioc(3, 22, unsafe.Sizeof(streamParm{}))
	vidiocSCtrl     = ioc(3, 28, unsafe.Sizeof(control{}))
	vidiocSExtCtrls = ioc(3, 72, unsafe.Sizeof(extControls{}))
)

func ioctl(fd int, req uintptr, arg unsafe.Pointer) error {
	for {
		_, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(arg))
		if e == syscall.EINTR {
			continue
		}
		if e != 0 {
			return e
		}
		return nil
	}
}

// v4l2Names is where the kernel names the video devices.
var v4l2Names = "/sys/class/video4linux"

// Find is /dev/videoN for the device named name, "" if there is none.
func Find(name string) string {
	paths, _ := filepath.Glob(filepath.Join(v4l2Names, "video*", "name"))
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil && strings.TrimSpace(string(b)) == name {
			return "/dev/" + filepath.Base(filepath.Dir(p))
		}
	}
	return ""
}

// Device is one open memory-to-memory device.
type Device struct{ fd int }

// Open opens the device named name (Find).
func Open(name string) (*Device, error) {
	path := Find(name)
	if path == "" {
		return nil, fmt.Errorf("v4l2m2m: no %s", name)
	}
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return &Device{fd: fd}, nil
}

// Close closes the device; its buffers go with it once unmapped.
func (d *Device) Close() { unix.Close(d.fd) }

// Format is a queue's format as the driver settled it: its size and each plane's bytes a line and size.
type Format struct {
	W, H   int
	Planes []PlaneFormat
}

// PlaneFormat is one plane's layout.
type PlaneFormat struct{ BytesPerLine, Size int }

// SetFormat sets a queue's format and returns what the driver made of it. sizes are the planes' sizes
// to ask for, where the driver takes them from the caller (a compressed format's buffer).
func (d *Device) SetFormat(typ uint32, w, h int, fourcc uint32, sizes ...int) (Format, error) {
	f := format{typ: typ}
	f.union.pix = pixFormatMplane{width: uint32(w), height: uint32(h), pixelformat: fourcc, field: 1}
	for i, n := range sizes {
		f.union.pix.planes[i].sizeimage = uint32(n)
	}
	if len(sizes) > 0 {
		f.union.pix.numPlanes = uint8(len(sizes))
	}
	if err := ioctl(d.fd, vidiocSFmt, unsafe.Pointer(&f)); err != nil {
		return Format{}, fmt.Errorf("S_FMT: %w", err)
	}
	if err := ioctl(d.fd, vidiocGFmt, unsafe.Pointer(&f)); err != nil {
		return Format{}, fmt.Errorf("G_FMT: %w", err)
	}
	p := f.union.pix
	out := Format{W: int(p.width), H: int(p.height)}
	for i := 0; i < int(p.numPlanes) && i < len(p.planes); i++ {
		out.Planes = append(out.Planes, PlaneFormat{int(p.planes[i].bytesperline), int(p.planes[i].sizeimage)})
	}
	return out, nil
}

// SetRate sets an output queue's frame rate (frames a second).
func (d *Device) SetRate(typ uint32, fps int) error {
	p := streamParm{typ: typ}
	p.union.num, p.union.den = 1, uint32(fps)
	return ioctl(d.fd, vidiocSParm, unsafe.Pointer(&p))
}

// SetCtrl sets a control.
func (d *Device) SetCtrl(id uint32, v int32) error {
	c := control{id, uint32(v)}
	return ioctl(d.fd, vidiocSCtrl, unsafe.Pointer(&c))
}

// SetCtrlArray sets an array control of 32-bit integers.
func (d *Device) SetCtrlArray(id uint32, v []int32) error {
	if len(v) == 0 {
		return errors.New("v4l2m2m: an empty array")
	}
	c := extControl{id: id, size: uint32(4 * len(v))}
	p := uintptr(unsafe.Pointer(&v[0]))
	for i := 0; i < int(unsafe.Sizeof(p)); i++ {
		c.value[i] = byte(p >> (8 * i))
	}
	cs := extControls{count: 1, controls: uintptr(unsafe.Pointer(&c))}
	return ioctl(d.fd, vidiocSExtCtrls, unsafe.Pointer(&cs))
}

// Buffer is one buffer of a queue: its planes mapped (memory MMAP) or to be given as dmabufs.
type Buffer struct {
	Index  int
	Planes [][]byte // mapped planes (MMAP)
}

// Request asks for n buffers of memory kind mem on queue typ, with planes planes each; MMAP ones are
// mapped.
func (d *Device) Request(typ, mem uint32, n, planes int) ([]*Buffer, error) {
	rb := requestBuffers{count: uint32(n), typ: typ, memory: mem}
	if err := ioctl(d.fd, vidiocReqbufs, unsafe.Pointer(&rb)); err != nil {
		return nil, fmt.Errorf("REQBUFS type %d memory %d count %d: %w", typ, mem, n, err)
	}
	bufs := make([]*Buffer, rb.count)
	for i := range bufs {
		bufs[i] = &Buffer{Index: i}
		if mem != MemoryMmap {
			continue
		}
		pl := make([]plane, planes)
		b := buffer{index: uint32(i), typ: typ, memory: mem, m: uintptr(unsafe.Pointer(&pl[0])), length: uint32(planes)}
		if err := ioctl(d.fd, vidiocQuerybuf, unsafe.Pointer(&b)); err != nil {
			return nil, fmt.Errorf("QUERYBUF: %w", err)
		}
		for j := 0; j < int(b.length); j++ {
			m, err := unix.Mmap(d.fd, int64(uint32(pl[j].m)), int(pl[j].length), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
			if err != nil {
				return nil, err
			}
			bufs[i].Planes = append(bufs[i].Planes, m)
		}
	}
	return bufs, nil
}

// Unmap lets a buffer's mappings go.
func (b *Buffer) Unmap() {
	for _, p := range b.Planes {
		_ = unix.Munmap(p)
	}
	b.Planes = nil
}

// Export is a dmabuf of each plane of an MMAP buffer, for another device to read in place.
func (d *Device) Export(typ uint32, b *Buffer) ([]int, error) {
	fds := make([]int, len(b.Planes))
	for j := range b.Planes {
		e := exportBuffer{typ: typ, index: uint32(b.Index), plane: uint32(j), flags: unix.O_RDWR | unix.O_CLOEXEC}
		if err := ioctl(d.fd, vidiocExpbuf, unsafe.Pointer(&e)); err != nil {
			for _, fd := range fds[:j] {
				unix.Close(fd)
			}
			return nil, fmt.Errorf("EXPBUF: %w", err)
		}
		fds[j] = int(e.fd)
	}
	return fds, nil
}

// Queue gives buffer index of queue typ to the driver: planes planes, each with used bytes (an input;
// nil for a result buffer, to be filled), and for memory DMABUF each plane's dmabuf.
func (d *Device) Queue(typ, mem uint32, index, planes int, used, fds []int) error {
	pl := make([]plane, planes)
	for j := range pl {
		if j < len(used) {
			pl[j].bytesused = uint32(used[j])
		}
		if j < len(fds) {
			pl[j].m = uintptr(uint32(int32(fds[j])))
			if j < len(used) {
				pl[j].length = uint32(used[j]) // vb2 checks the bytes used against it
			}
		}
	}
	b := buffer{index: uint32(index), typ: typ, memory: mem, m: uintptr(unsafe.Pointer(&pl[0])), length: uint32(planes)}
	return ioctl(d.fd, vidiocQbuf, unsafe.Pointer(&b))
}

// Done is a buffer the driver gave back: which, the bytes used in each plane, and whether it holds a
// keyframe.
type Done struct {
	Index    int
	Used     []int
	Keyframe bool
}

// Dequeue waits for the next buffer the driver is done with on queue typ.
func (d *Device) Dequeue(typ, mem uint32) (Done, error) {
	pl := make([]plane, 8)
	b := buffer{typ: typ, memory: mem, m: uintptr(unsafe.Pointer(&pl[0])), length: uint32(len(pl))}
	if err := ioctl(d.fd, vidiocDqbuf, unsafe.Pointer(&b)); err != nil {
		return Done{}, fmt.Errorf("DQBUF: %w", err)
	}
	if b.flags&flagError != 0 {
		return Done{Index: int(b.index)}, errors.New("v4l2m2m: the device failed the buffer")
	}
	done := Done{Index: int(b.index), Keyframe: b.flags&flagKeyframe != 0}
	for j := 0; j < int(b.length) && j < len(pl); j++ {
		done.Used = append(done.Used, int(pl[j].bytesused))
	}
	return done, nil
}

// Stream starts (or stops) a queue.
func (d *Device) Stream(typ uint32, on bool) error {
	t := int32(typ)
	req := vidiocStreamon
	if !on {
		req = vidiocStreamoff
	}
	return ioctl(d.fd, req, unsafe.Pointer(&t))
}
