//go:build linux

// Package jpegenc is the 2nd gen Show 8's hardware JPEG encoder: the MT8183's JPGENC block, which
// mainline's mtk-jpeg driver offers as a V4L2 memory-to-memory device named mtk-jpeg-enc. A picture
// goes in as YUYV and comes back a JPEG, with the CPU doing no more than the color conversion, which
// is far cheaper in time and power than encoding on the cores.
//
// The device is the multi-planar M2M kind: an OUTPUT queue the picture is written to and a CAPTURE
// queue the JPEG is read from, one buffer each, both mapped once and reused for every picture of the
// same size. The structures passed to the kernel are declared as Go structs whose layout follows the
// architecture the way the C ones do (pointers and longs are words), so the request numbers, which
// carry their sizes, are right on arm and arm64 alike.
package jpegenc

import (
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	bufTypeOutputMplane  = 10 // V4L2_BUF_TYPE_VIDEO_OUTPUT_MPLANE
	bufTypeCaptureMplane = 9  // V4L2_BUF_TYPE_VIDEO_CAPTURE_MPLANE
	memoryMmap           = 1
	memoryDMABuf         = 4
	fieldNone            = 1
	pixYUYV              = 'Y' | 'U'<<8 | 'Y'<<16 | 'V'<<24
	pixJPEG              = 'J' | 'P'<<8 | 'E'<<16 | 'G'<<24
	cidJPEGQuality       = 0x009d0903 // V4L2_CID_JPEG_COMPRESSION_QUALITY

	// encodeWait is how long a picture may take: a 13 MP one takes a fraction of it.
	encodeWait = 2 * time.Second
)

// format is struct v4l2_format: the type, then a 200-byte union that holds pointers in some of its
// members, so it starts on a word boundary.
type format struct {
	typ   uint32
	union struct {
		_   [0]uintptr
		pix pixFormatMplane
		_   [200 - unsafe.Sizeof(pixFormatMplane{})]byte
	}
}

// pixFormatMplane is struct v4l2_pix_format_mplane.
type pixFormatMplane struct {
	width, height, pixelformat, field, colorspace uint32
	planes                                        [8]planePixFormat
	numPlanes, flags, ycbcr, quantization, xfer   uint8
	_                                             [7]uint8
}

// planePixFormat is struct v4l2_plane_pix_format.
type planePixFormat struct {
	sizeimage, bytesperline uint32
	_                       [6]uint16
}

// requestBuffers is struct v4l2_requestbuffers.
type requestBuffers struct {
	count, typ, memory, capabilities uint32
	flags                            uint8
	_                                [3]uint8
}

// plane is struct v4l2_plane: m is a union of a 32-bit offset, an unsigned long and an int.
type plane struct {
	bytesused, length uint32
	m                 uintptr
	dataOffset        uint32
	_                 [11]uint32
}

// buffer is struct v4l2_buffer: m holds the planes' address for the multi-planar types.
type buffer struct {
	index, typ, bytesused, flags, field uint32
	timestamp                           unix.Timeval
	timecode                            [16]byte
	sequence, memory                    uint32
	m                                   uintptr
	length, _                           uint32
	requestFD                           int32
}

// control is struct v4l2_control.
type control struct {
	id    uint32
	value int32
}

func ioc(dir, nr, size uintptr) uintptr { return dir<<30 | size<<16 | 'V'<<8 | nr }

var (
	vidiocSFmt      = ioc(3, 5, unsafe.Sizeof(format{}))
	vidiocReqbufs   = ioc(3, 8, unsafe.Sizeof(requestBuffers{}))
	vidiocQuerybuf  = ioc(3, 9, unsafe.Sizeof(buffer{}))
	vidiocQbuf      = ioc(3, 15, unsafe.Sizeof(buffer{}))
	vidiocDqbuf     = ioc(3, 17, unsafe.Sizeof(buffer{}))
	vidiocStreamon  = ioc(1, 18, unsafe.Sizeof(int32(0)))
	vidiocStreamoff = ioc(1, 19, unsafe.Sizeof(int32(0)))
	vidiocSCtrl     = ioc(3, 28, unsafe.Sizeof(control{}))
)

func ioctl(fd int, req uintptr, arg unsafe.Pointer) error {
	for {
		_, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(arg))
		if e == syscall.EINTR {
			continue
		}
		if e != 0 {
			return e
		}
		return nil
	}
}

// devicePath is the encoder's node, found by its name; empty on a board without one.
func devicePath() string {
	names, _ := filepath.Glob("/sys/class/video4linux/video*/name")
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err == nil && strings.TrimSpace(string(b)) == "mtk-jpeg-enc" {
			return "/dev/" + filepath.Base(filepath.Dir(n))
		}
	}
	return ""
}

var (
	findOnce sync.Once
	path     string
)

// Available reports whether this board has the encoder.
func Available() bool {
	findOnce.Do(func() { path = devicePath() })
	return path != ""
}

// Fits reports whether the encoder takes a picture of this size: YUYV wants the width a multiple of 32
// and the height of 8 (mtk-jpeg's h_align and v_align for it on the MT8183).
func Fits(r image.Rectangle) bool {
	return r.Dx()%32 == 0 && r.Dy()%8 == 0 && r.Dx() > 0 && r.Dy() > 0
}

// session is the device opened for one picture size, with its two buffers: the JPEG's mapped, and the
// picture's either mapped too or, with stride set, a dmabuf of the GPU's imported at each encode.
type session struct {
	fd       int
	w, h     int
	in, out  []byte
	quality  int
	stride   int // >0: the picture comes as a dmabuf with this many bytes a row
	streamed bool
}

var (
	mu   sync.Mutex
	open *session
)

// Encode makes a JPEG of img in hardware. One picture at a time: the device has one buffer a side.
func Encode(img *image.RGBA, quality int) ([]byte, error) {
	return encode(img.Bounds().Dx(), img.Bounds().Dy(), quality, func(dst []byte) { toYUYV(img, dst) })
}

// EncodeDMABuf makes a JPEG of a YUYV picture in a dmabuf (the GPU's), stride bytes a row, which the
// encoder reads where it is. The buffer must hold the picture until this returns.
func EncodeDMABuf(dmabuf, w, h, stride, quality int) ([]byte, error) {
	return encodeWith(w, h, quality, stride, dmabuf, nil)
}

func encode(w, h, quality int, fill func(dst []byte)) ([]byte, error) {
	return encodeWith(w, h, quality, 0, -1, fill)
}

func encodeWith(w, h, quality, stride, dmabuf int, fill func(dst []byte)) ([]byte, error) {
	if !Available() {
		return nil, errors.New("jpegenc: no hardware encoder")
	}
	if !Fits(image.Rect(0, 0, w, h)) {
		return nil, fmt.Errorf("jpegenc: %dx%d is not a size the encoder takes", w, h)
	}
	mu.Lock()
	defer mu.Unlock()
	if open != nil && (open.w != w || open.h != h || open.quality != quality || open.stride != stride) {
		open.close()
		open = nil
	}
	if open == nil {
		s, err := start(w, h, quality, stride)
		if err != nil {
			return nil, err
		}
		open = s
	}
	if fill != nil {
		fill(open.in)
	}
	jpg, err := open.encode(dmabuf)
	if err != nil {
		// A device in a bad state is started afresh next time.
		open.close()
		open = nil
	}
	return jpg, err
}

func start(w, h, quality, stride int) (*session, error) {
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("jpegenc: %w", err)
	}
	s := &session{fd: fd, w: w, h: h, quality: quality, stride: stride}
	if err := s.setup(); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

func (s *session) setup() error {
	in := format{typ: bufTypeOutputMplane}
	in.union.pix = pixFormatMplane{width: uint32(s.w), height: uint32(s.h), pixelformat: pixYUYV, field: fieldNone, numPlanes: 1}
	in.union.pix.planes[0].bytesperline = uint32(s.w * 2)
	if s.stride > 0 {
		in.union.pix.planes[0].bytesperline = uint32(s.stride)
		in.union.pix.planes[0].sizeimage = uint32(s.stride * s.h)
	}
	if err := ioctl(s.fd, vidiocSFmt, unsafe.Pointer(&in)); err != nil {
		return fmt.Errorf("jpegenc: picture format: %w", err)
	}
	if in.union.pix.width != uint32(s.w) || in.union.pix.height != uint32(s.h) || in.union.pix.pixelformat != pixYUYV {
		return fmt.Errorf("jpegenc: the encoder took %dx%d", in.union.pix.width, in.union.pix.height)
	}
	out := format{typ: bufTypeCaptureMplane}
	out.union.pix = pixFormatMplane{width: uint32(s.w), height: uint32(s.h), pixelformat: pixJPEG, field: fieldNone, numPlanes: 1}
	// Room for the worst case. The driver's default is 1 MB, which a noisy 2112x1568 frame at quality
	// 90 overflows, and an overflow ends the encode in a stall rather than a JPEG.
	out.union.pix.planes[0].sizeimage = uint32(s.w * s.h * 3 / 2)
	if err := ioctl(s.fd, vidiocSFmt, unsafe.Pointer(&out)); err != nil {
		return fmt.Errorf("jpegenc: jpeg format: %w", err)
	}
	q := control{id: cidJPEGQuality, value: int32(s.quality)}
	if err := ioctl(s.fd, vidiocSCtrl, unsafe.Pointer(&q)); err != nil {
		return fmt.Errorf("jpegenc: quality: %w", err)
	}
	var err error
	if s.stride > 0 {
		rb := requestBuffers{count: 1, typ: bufTypeOutputMplane, memory: memoryDMABuf}
		if err := ioctl(s.fd, vidiocReqbufs, unsafe.Pointer(&rb)); err != nil || rb.count < 1 {
			return fmt.Errorf("jpegenc: dmabuf buffers: %v (%d)", err, rb.count)
		}
	} else if s.in, err = s.mapBuffer(bufTypeOutputMplane); err != nil {
		return err
	}
	if s.out, err = s.mapBuffer(bufTypeCaptureMplane); err != nil {
		return err
	}
	for _, t := range []int32{bufTypeOutputMplane, bufTypeCaptureMplane} {
		if err := ioctl(s.fd, vidiocStreamon, unsafe.Pointer(&t)); err != nil {
			return fmt.Errorf("jpegenc: stream on: %w", err)
		}
	}
	s.streamed = true
	return nil
}

// mapBuffer asks for one buffer of the type and maps its plane.
func (s *session) mapBuffer(typ uint32) ([]byte, error) {
	rb := requestBuffers{count: 1, typ: typ, memory: memoryMmap}
	if err := ioctl(s.fd, vidiocReqbufs, unsafe.Pointer(&rb)); err != nil || rb.count < 1 {
		return nil, fmt.Errorf("jpegenc: buffers: %v (%d)", err, rb.count)
	}
	var p [1]plane
	b := buffer{typ: typ, memory: memoryMmap, m: uintptr(unsafe.Pointer(&p[0])), length: 1}
	if err := ioctl(s.fd, vidiocQuerybuf, unsafe.Pointer(&b)); err != nil {
		return nil, fmt.Errorf("jpegenc: query buffer: %w", err)
	}
	runtime.KeepAlive(&p)
	m, err := unix.Mmap(s.fd, int64(uint32(p[0].m)), int(p[0].length), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("jpegenc: map: %w", err)
	}
	return m, nil
}

// encode queues the picture (in s.in, or the dmabuf) and an empty JPEG buffer, and waits for the JPEG.
func (s *session) encode(dmabuf int) ([]byte, error) {
	var pin, pout [1]plane
	pin[0].bytesused = uint32(s.w * s.h * 2)
	pin[0].length = uint32(len(s.in))
	memory := uint32(memoryMmap)
	if s.stride > 0 {
		memory = memoryDMABuf
		pin[0].m = uintptr(dmabuf)
		pin[0].bytesused = uint32(s.stride * s.h)
		pin[0].length = uint32(s.stride * s.h)
	}
	pout[0].length = uint32(len(s.out))
	in := buffer{typ: bufTypeOutputMplane, memory: memory, m: uintptr(unsafe.Pointer(&pin[0])), length: 1, field: fieldNone}
	out := buffer{typ: bufTypeCaptureMplane, memory: memoryMmap, m: uintptr(unsafe.Pointer(&pout[0])), length: 1}
	if err := ioctl(s.fd, vidiocQbuf, unsafe.Pointer(&out)); err != nil {
		return nil, fmt.Errorf("jpegenc: queue jpeg buffer: %w", err)
	}
	if err := ioctl(s.fd, vidiocQbuf, unsafe.Pointer(&in)); err != nil {
		return nil, fmt.Errorf("jpegenc: queue picture: %w", err)
	}
	// The JPEG is ready when the device polls readable. A picture the encoder never finishes must not
	// hold the camera up: past the wait the session is dropped and the caller encodes on the cores.
	poll := []unix.PollFd{{Fd: int32(s.fd), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(poll, int(encodeWait.Milliseconds()))
		if err == unix.EINTR {
			continue
		}
		if err != nil || n == 0 {
			return nil, fmt.Errorf("jpegenc: no jpeg within %v (%v)", encodeWait, err)
		}
		break
	}
	var dout [1]plane
	got := buffer{typ: bufTypeCaptureMplane, memory: memoryMmap, m: uintptr(unsafe.Pointer(&dout[0])), length: 1}
	if err := ioctl(s.fd, vidiocDqbuf, unsafe.Pointer(&got)); err != nil {
		return nil, fmt.Errorf("jpegenc: take jpeg: %w", err)
	}
	var din [1]plane
	done := buffer{typ: bufTypeOutputMplane, memory: memory, m: uintptr(unsafe.Pointer(&din[0])), length: 1}
	if err := ioctl(s.fd, vidiocDqbuf, unsafe.Pointer(&done)); err != nil {
		return nil, fmt.Errorf("jpegenc: take picture back: %w", err)
	}
	runtime.KeepAlive(&pin)
	runtime.KeepAlive(&pout)
	runtime.KeepAlive(&dout)
	runtime.KeepAlive(&din)
	if got.flags&0x40 != 0 { // V4L2_BUF_FLAG_ERROR
		return nil, errors.New("jpegenc: the encoder flagged the picture")
	}
	n := int(dout[0].bytesused) - int(dout[0].dataOffset)
	if n <= 0 || int(dout[0].bytesused) > len(s.out) {
		return nil, fmt.Errorf("jpegenc: %d bytes back", dout[0].bytesused)
	}
	jpg := make([]byte, n)
	copy(jpg, s.out[dout[0].dataOffset:dout[0].bytesused])
	return jpg, nil
}

func (s *session) close() {
	if s.streamed {
		for _, t := range []int32{bufTypeOutputMplane, bufTypeCaptureMplane} {
			ioctl(s.fd, vidiocStreamoff, unsafe.Pointer(&t))
		}
	}
	if s.in != nil {
		unix.Munmap(s.in)
	}
	if s.out != nil {
		unix.Munmap(s.out)
	}
	syscall.Close(s.fd)
}

// toYUYV converts the picture to YUYV (BT.601, full range, as JFIF has it), two pixels a step, in
// bands across the cores. The chroma of each pair is the average of the two pixels'.
func toYUYV(img *image.RGBA, dst []byte) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	bands := runtime.NumCPU()
	var wg sync.WaitGroup
	for b := range bands {
		y0, y1 := h*b/bands, h*(b+1)/bands
		wg.Add(1)
		go func() {
			defer wg.Done()
			for y := y0; y < y1; y++ {
				src := img.Pix[y*img.Stride : y*img.Stride+w*4]
				out := dst[y*w*2 : (y+1)*w*2]
				for x := 0; x < w; x += 2 {
					r0, g0, b0 := int32(src[x*4]), int32(src[x*4+1]), int32(src[x*4+2])
					r1, g1, b1 := int32(src[x*4+4]), int32(src[x*4+5]), int32(src[x*4+6])
					l0 := (19595*r0 + 38470*g0 + 7471*b0 + 1<<15) >> 16
					l1 := (19595*r1 + 38470*g1 + 7471*b1 + 1<<15) >> 16
					r, g, bl := r0+r1, g0+g1, b0+b1
					cb := (-11059*r - 21709*g + 32768*bl + 257<<16) >> 17
					cr := (32768*r - 27439*g - 5329*bl + 257<<16) >> 17
					out[x*2], out[x*2+1], out[x*2+2], out[x*2+3] = clamp(l0), clamp(cb), clamp(l1), clamp(cr)
				}
			}
		}()
	}
	wg.Wait()
}

func clamp(v int32) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}
