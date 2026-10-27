//go:build linux

// Package jpegenc is the 2nd gen Show 8's hardware JPEG encoder: the MT8183's JPEG encoder, which
// mainline's mtk-jpeg driver offers as a V4L2 memory-to-memory device named mtk-jpeg-enc (in the athena
// kernel since CONFIG_VIDEO_MEDIATEK_JPEG). A picture goes in as YUYV and comes back a baseline JPEG,
// with the cores doing no more than the color conversion, which is far cheaper in time and power than
// encoding on them.
//
// The device is the multi-planar M2M kind: an OUTPUT queue the picture is written to and a CAPTURE
// queue the JPEG is read from. Each open of the device is a context of its own, so a session is kept
// for each size and quality in use (the camera pages' and the stream's), each with one buffer a side,
// mapped once and reused for every picture. The structures passed to the kernel are declared as Go
// structs whose layout follows the architecture the way the C ones do (pointers and longs are words),
// so the request numbers, which carry their sizes, are right on arm and arm64 alike.
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
	fieldNone            = 1
	bufFlagError         = 0x40 // V4L2_BUF_FLAG_ERROR
	pixYUYV              = 'Y' | 'U'<<8 | 'Y'<<16 | 'V'<<24
	pixJPEG              = 'J' | 'P'<<8 | 'E'<<16 | 'G'<<24
	cidJPEGQuality       = 0x009d0903 // V4L2_CID_JPEG_COMPRESSION_QUALITY

	// encodeWait is how long a picture may take: a 13 MP one takes a fraction of it.
	encodeWait = 2 * time.Second

	// sessions is how many sizes and qualities are kept open at once: the camera pages' and the stream's.
	sessions = 2

	// backOff is how long the encoder is left alone after it fails a picture: an encoder that hangs
	// would otherwise hold every picture for encodeWait before it went to the cores.
	backOff = time.Minute
)

// format is struct v4l2_format: the type, then a 200-byte union that holds pointers in some of its
// members, so it starts on a word boundary: 204 bytes on arm, 208 on arm64.
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

// buffer is struct v4l2_buffer: m holds the planes' address for the multi-planar types. The timestamp
// is a struct timeval, two longs: what a 32-bit caller hands the 64-bit kernel's compat layer too.
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

// sysfs is where the node is looked up; a test points it elsewhere.
var sysfs = "/sys/class/video4linux"

// devicePath is the encoder's node, found by its name; empty on a board without one.
func devicePath() string {
	names, _ := filepath.Glob(sysfs + "/video*/name")
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

// Available reports whether this board has the encoder. It is looked for once.
func Available() bool {
	findOnce.Do(func() { path = devicePath() })
	return path != ""
}

// Fits reports whether the encoder takes a picture of this size: YUYV wants the width a multiple of 32
// and the height of 8 (mtk-jpeg's alignment for it on the MT8183).
func Fits(r image.Rectangle) bool {
	return r.Dx() > 0 && r.Dy() > 0 && r.Dx()%32 == 0 && r.Dy()%8 == 0
}

// session is the device opened for one picture size and quality, with its two buffers mapped.
type session struct {
	mu       sync.Mutex // one picture at a time: one buffer a side
	fd       int
	w, h     int
	quality  int
	in, out  []byte
	streamed bool
	broken   bool // failed a picture: closed once nobody uses it, and opened afresh next time
	users    int
	used     time.Time
}

var (
	mu   sync.Mutex
	open []*session
)

// Encode makes a JPEG of img at quality in hardware, converting it to YUYV on the cores first.
func Encode(img *image.RGBA, quality int) ([]byte, error) {
	b := img.Bounds()
	return EncodeYUYV(b.Dx(), b.Dy(), quality, func(dst []byte) { toYUYV(img, dst) })
}

// EncodeYUYV makes a JPEG of a w x h picture that fill writes into the encoder's buffer as YUYV, w*2
// bytes a row. Pictures of the same size and quality take turns; others go at the same time.
func EncodeYUYV(w, h, quality int, fill func(dst []byte)) ([]byte, error) {
	if !Available() {
		return nil, errors.New("jpegenc: no hardware encoder")
	}
	if !Fits(image.Rect(0, 0, w, h)) {
		return nil, fmt.Errorf("jpegenc: %dx%d is not a size the encoder takes", w, h)
	}
	if until := failed.until(); now().Before(until) {
		return nil, fmt.Errorf("%w until %s", ErrBackingOff, until.Format(time.TimeOnly))
	}
	jpg, err := attempt(w, h, quality, fill)
	if err != nil {
		failed.set(now().Add(backOff))
	}
	return jpg, err
}

// ErrBackingOff is what Encode and EncodeYUYV answer at once for backOff after the encoder failed a
// picture, so callers go to the cores without waiting on it.
var ErrBackingOff = errors.New("jpegenc: backing off after a failure")

// failed is when the encoder may be tried again after a failure.
var failed backoffUntil

type backoffUntil struct {
	mu sync.Mutex
	t  time.Time
}

func (b *backoffUntil) until() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.t
}

func (b *backoffUntil) set(t time.Time) {
	b.mu.Lock()
	b.t = t
	b.mu.Unlock()
}

// now and attempt are the clock and one picture through the device; tests replace them.
var (
	now     = time.Now
	attempt = encodeOnDevice
)

// encodeOnDevice is one picture through the session for its size and quality.
func encodeOnDevice(w, h, quality int, fill func(dst []byte)) ([]byte, error) {
	s, err := take(w, h, quality)
	if err != nil {
		return nil, err
	}
	defer give(s)
	s.mu.Lock()
	defer s.mu.Unlock()
	fill(s.in)
	jpg, err := s.encode()
	if err != nil {
		mu.Lock()
		s.broken = true
		mu.Unlock()
	}
	return jpg, err
}

// take is the open session for the size and quality, opened if there is none. Past sessions, the one
// least recently used that nobody is using is closed.
func take(w, h, quality int) (*session, error) {
	mu.Lock()
	defer mu.Unlock()
	for _, s := range open {
		if s.w == w && s.h == h && s.quality == quality && !s.broken {
			s.users++
			s.used = time.Now()
			return s, nil
		}
	}
	if len(open) >= sessions {
		idle := -1
		for i, s := range open {
			if s.users == 0 && (idle < 0 || s.used.Before(open[idle].used)) {
				idle = i
			}
		}
		if idle >= 0 {
			open[idle].close()
			open = append(open[:idle], open[idle+1:]...)
		}
	}
	s, err := start(w, h, quality)
	if err != nil {
		return nil, err
	}
	s.users, s.used = 1, time.Now()
	open = append(open, s)
	return s, nil
}

// give hands a session back; a broken one is closed with its last user.
func give(s *session) {
	mu.Lock()
	defer mu.Unlock()
	s.users--
	if !s.broken || s.users > 0 {
		return
	}
	for i, o := range open {
		if o == s {
			open = append(open[:i], open[i+1:]...)
			break
		}
	}
	s.close()
}

func start(w, h, quality int) (*session, error) {
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("jpegenc: %w", err)
	}
	s := &session{fd: fd, w: w, h: h, quality: quality}
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
	if err := ioctl(s.fd, vidiocSFmt, unsafe.Pointer(&in)); err != nil {
		return fmt.Errorf("jpegenc: picture format: %w", err)
	}
	if in.union.pix.width != uint32(s.w) || in.union.pix.height != uint32(s.h) || in.union.pix.pixelformat != pixYUYV ||
		in.union.pix.planes[0].bytesperline != uint32(s.w*2) {
		return fmt.Errorf("jpegenc: the encoder took %dx%d, %d bytes a row", in.union.pix.width, in.union.pix.height,
			in.union.pix.planes[0].bytesperline)
	}
	out := format{typ: bufTypeCaptureMplane}
	out.union.pix = pixFormatMplane{width: uint32(s.w), height: uint32(s.h), pixelformat: pixJPEG, field: fieldNone, numPlanes: 1}
	// Room for the worst case. The driver's default is 1 MB, which a noisy 2112x1568 frame at quality
	// 90 overflows, and an overflow ends the encode in an error rather than a JPEG.
	out.union.pix.planes[0].sizeimage = uint32(s.w * s.h * 3 / 2)
	if err := ioctl(s.fd, vidiocSFmt, unsafe.Pointer(&out)); err != nil {
		return fmt.Errorf("jpegenc: jpeg format: %w", err)
	}
	q := control{id: cidJPEGQuality, value: int32(s.quality)}
	if err := ioctl(s.fd, vidiocSCtrl, unsafe.Pointer(&q)); err != nil {
		return fmt.Errorf("jpegenc: quality: %w", err)
	}
	var err error
	if s.in, err = s.mapBuffer(bufTypeOutputMplane); err != nil {
		return err
	}
	if len(s.in) < s.w*s.h*2 {
		return fmt.Errorf("jpegenc: a %d-byte picture buffer for %dx%d", len(s.in), s.w, s.h)
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

// encode queues the picture in s.in and an empty JPEG buffer, and waits for the JPEG.
func (s *session) encode() ([]byte, error) {
	var pin, pout [1]plane
	pin[0].bytesused = uint32(s.w * s.h * 2)
	pin[0].length = uint32(len(s.in))
	pout[0].length = uint32(len(s.out))
	in := buffer{typ: bufTypeOutputMplane, memory: memoryMmap, m: uintptr(unsafe.Pointer(&pin[0])), length: 1, field: fieldNone}
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
	deadline := time.Now().Add(encodeWait)
	for {
		n, err := unix.Poll(poll, int(time.Until(deadline).Milliseconds()))
		if err == unix.EINTR && time.Now().Before(deadline) {
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
	done := buffer{typ: bufTypeOutputMplane, memory: memoryMmap, m: uintptr(unsafe.Pointer(&din[0])), length: 1}
	if err := ioctl(s.fd, vidiocDqbuf, unsafe.Pointer(&done)); err != nil {
		return nil, fmt.Errorf("jpegenc: take picture back: %w", err)
	}
	runtime.KeepAlive(&pin)
	runtime.KeepAlive(&pout)
	runtime.KeepAlive(&dout)
	runtime.KeepAlive(&din)
	if got.flags&bufFlagError != 0 {
		return nil, errors.New("jpegenc: the encoder flagged the picture")
	}
	return jpegOf(s.out, dout[0].bytesused, dout[0].dataOffset)
}

// jpegOf copies the JPEG out of the mapped buffer: bytesused counts from the start of the plane, the
// JPEG from dataOffset. The encoder pads what it writes with zeros past the end marker, to a size of its
// own; they are left behind, so the JPEG ends at its end marker, as RTP/JPEG wants it.
func jpegOf(buf []byte, used, offset uint32) ([]byte, error) {
	if used <= offset || int(used) > len(buf) {
		return nil, fmt.Errorf("jpegenc: %d bytes back at %d", used, offset)
	}
	src := buf[offset:used]
	end := len(src)
	for end > 0 && src[end-1] == 0 {
		end--
	}
	if end < 4 || src[0] != 0xFF || src[1] != 0xD8 || src[end-2] != 0xFF || src[end-1] != 0xD9 {
		return nil, errors.New("jpegenc: what came back is not a whole JPEG")
	}
	return append([]byte(nil), src[:end]...), nil
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
	bands := min(runtime.NumCPU(), h)
	var wg sync.WaitGroup
	for b := range bands {
		y0, y1 := h*b/bands, h*(b+1)/bands
		wg.Add(1)
		go func() {
			defer wg.Done()
			for y := y0; y < y1; y++ {
				src := img.Pix[y*img.Stride : y*img.Stride+w*4]
				out := dst[y*w*2 : (y+1)*w*2]
				for x := 0; x+1 < w; x += 2 {
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
