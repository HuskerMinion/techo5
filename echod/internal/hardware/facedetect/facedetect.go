//go:build linux

// Package facedetect finds faces in the 2nd gen Show 8's camera frames with the MT8183's own blocks:
// the ISP's pass 2 (the DIP, /dev/video* "mtk-dip") develops the sensor's packed RAW10 frame into a
// small YUYV picture, and the face detector (the FDVT, "mtk-fd-4.0") finds the faces in it. Both are
// V4L2 memory-to-memory devices of the board's kernel (custom-kernel/athena: patches/mdp3-dip,
// patches/mtk-fd); the SCP plans their work and the AP programs them (patches/scp-mmproxy). The cores
// copy the frame in and read the faces out, nothing more: a detection takes about 40 ms of the blocks'
// time.
//
// The structures passed to the kernel are declared as Go structs laid out as the C ones are on the
// architecture (pointers and longs are words), as hardware/jpegenc's are.
package facedetect

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The picture the detector looks at: as large as it takes (640x480), the frame's shape kept.
const detW = 640

// Face is a face found: where, in the picture the detector saw (Size), and how sure it is (the
// detector's own score; a face the merging kept counts the candidates it stood for in Votes).
type Face struct {
	Box   image.Rectangle
	Score int
	Votes int
}

const (
	bufTypeCaptureMplane = 9  // V4L2_BUF_TYPE_VIDEO_CAPTURE_MPLANE
	bufTypeOutputMplane  = 10 // V4L2_BUF_TYPE_VIDEO_OUTPUT_MPLANE
	bufTypeMetaCapture   = 13 // V4L2_BUF_TYPE_META_CAPTURE
	memoryMmap           = 1
	pixYUYV              = 'Y' | 'U'<<8 | 'Y'<<16 | 'V'<<24
	pixPBAA              = 'p' | 'B'<<8 | 'A'<<16 | 'A'<<24 // packed 10-bit BGGR, the sensor's

	cidUserBase      = 0x00980900
	cidBlackLevel    = cidUserBase + 11
	cidRedBalance    = cidUserBase + 14
	cidBlueBalance   = cidUserBase + 15
	cidDigitalGain   = 0x009f0905           // V4L2_CID_DIGITAL_GAIN (the image processing class)
	cidFDBase        = cidUserBase + 0x1fd0 // V4L2_CID_USER_MTK_FD_BASE
	cidFDScaleWidth  = cidFDBase + 2
	cidFDScaleHeight = cidFDBase + 3
	cidFDScaleNum    = cidFDBase + 4

	fdScales     = 15   // the detector's pyramid, at most
	fdResultSize = 16   // a face's record
	fdMaxFaces   = 1026 // records in the result, then the count
)

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
	m                 uintptr
	dataOffset        uint32
	_                 [11]uint32
}

type buffer struct {
	index, typ, bytesused, flags, field uint32
	timestamp                           unix.Timeval
	timecode                            [16]byte
	sequence, memory                    uint32
	m                                   uintptr // offset (single-planar) or the planes (multi-planar)
	length, reserved2                   uint32
	requestFD                           int32
}

type control struct{ id, value uint32 }

// extControl is struct v4l2_ext_control, which is packed: id, size, reserved, then an 8-byte union
// (a value or a pointer).
type extControl [20]byte

// extControls is struct v4l2_ext_controls.
type extControls struct {
	which, count, errorIdx uint32
	requestFD              int32
	_                      uint32
	controls               uintptr
}

func ioc(dir, nr, size uintptr) uintptr { return dir<<30 | size<<16 | 'V'<<8 | nr }

var (
	vidiocSFmt      = ioc(3, 5, unsafe.Sizeof(format{}))
	vidiocReqbufs   = ioc(3, 8, unsafe.Sizeof(requestBuffers{}))
	vidiocQuerybuf  = ioc(3, 9, unsafe.Sizeof(buffer{}))
	vidiocQbuf      = ioc(3, 15, unsafe.Sizeof(buffer{}))
	vidiocDqbuf     = ioc(3, 17, unsafe.Sizeof(buffer{}))
	vidiocStreamon  = ioc(1, 18, unsafe.Sizeof(int32(0)))
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

// device is /dev/videoN for the device named name, "" if there is none.
func device(name string) string {
	paths, _ := filepath.Glob(filepath.Join(v4l2Names, "video*", "name"))
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil && strings.TrimSpace(string(b)) == name {
			return "/dev/" + filepath.Base(filepath.Dir(p))
		}
	}
	return ""
}

var available = sync.OnceValue(func() bool { return device("mtk-dip") != "" && device("mtk-fd-4.0") != "" })

// Available is whether this board has both blocks.
func Available() bool { return available() }

// m2m is one memory-to-memory device with one buffer each way, mapped once.
type m2m struct {
	fd       int
	in, out  []byte
	outMulti bool // the result queue is multi-planar (the DIP's) rather than meta (the detector's)
}

func open(name string) (*m2m, error) {
	path := device(name)
	if path == "" {
		return nil, fmt.Errorf("facedetect: no %s", name)
	}
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return &m2m{fd: fd}, nil
}

func (m *m2m) setFmt(typ, w, h, pix uint32) error {
	f := format{typ: typ}
	f.union.pix = pixFormatMplane{width: w, height: h, pixelformat: pix, field: 1, numPlanes: 1}
	return ioctl(m.fd, vidiocSFmt, unsafe.Pointer(&f))
}

func (m *m2m) ctrl(id, v uint32) error {
	c := control{id, v}
	return ioctl(m.fd, vidiocSCtrl, unsafe.Pointer(&c))
}

// buffers asks for and maps one buffer on each queue, and starts both.
func (m *m2m) buffers(outTyp uint32) error {
	for _, typ := range []uint32{bufTypeOutputMplane, outTyp} {
		rb := requestBuffers{count: 1, typ: typ, memory: memoryMmap}
		if err := ioctl(m.fd, vidiocReqbufs, unsafe.Pointer(&rb)); err != nil {
			return fmt.Errorf("REQBUFS: %w", err)
		}
		var pl [1]plane
		b := buffer{typ: typ, memory: memoryMmap}
		if typ != bufTypeMetaCapture {
			b.m, b.length = uintptr(unsafe.Pointer(&pl[0])), 1
		}
		if err := ioctl(m.fd, vidiocQuerybuf, unsafe.Pointer(&b)); err != nil {
			return fmt.Errorf("QUERYBUF: %w", err)
		}
		length, offset := b.length, int64(uint32(b.m))
		if typ != bufTypeMetaCapture {
			length, offset = pl[0].length, int64(uint32(pl[0].m))
		}
		mem, err := unix.Mmap(m.fd, offset, int(length), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
		if err != nil {
			return err
		}
		if typ == bufTypeOutputMplane {
			m.in = mem
		} else {
			m.out = mem
		}
	}
	for _, typ := range []int32{bufTypeOutputMplane, int32(outTyp)} {
		if err := ioctl(m.fd, vidiocStreamon, unsafe.Pointer(&typ)); err != nil {
			return fmt.Errorf("STREAMON: %w", err)
		}
	}
	m.outMulti = outTyp == bufTypeCaptureMplane
	return nil
}

// run queues the input (n bytes of it) and the result buffer, and waits for the result.
func (m *m2m) run(n int, outTyp uint32) (int, error) {
	var ipl, opl [1]plane
	ipl[0].bytesused = uint32(n)
	ib := buffer{typ: bufTypeOutputMplane, memory: memoryMmap, m: uintptr(unsafe.Pointer(&ipl[0])), length: 1}
	ob := buffer{typ: outTyp, memory: memoryMmap}
	if m.outMulti {
		ob.m, ob.length = uintptr(unsafe.Pointer(&opl[0])), 1
	}
	if err := ioctl(m.fd, vidiocQbuf, unsafe.Pointer(&ib)); err != nil {
		return 0, fmt.Errorf("QBUF in: %w", err)
	}
	if err := ioctl(m.fd, vidiocQbuf, unsafe.Pointer(&ob)); err != nil {
		return 0, fmt.Errorf("QBUF out: %w", err)
	}
	// Both come back: the result (blocking until the blocks are done) and the input
	if err := ioctl(m.fd, vidiocDqbuf, unsafe.Pointer(&ob)); err != nil {
		return 0, fmt.Errorf("DQBUF out: %w", err)
	}
	ib = buffer{typ: bufTypeOutputMplane, memory: memoryMmap, m: uintptr(unsafe.Pointer(&ipl[0])), length: 1}
	if err := ioctl(m.fd, vidiocDqbuf, unsafe.Pointer(&ib)); err != nil {
		return 0, fmt.Errorf("DQBUF in: %w", err)
	}
	if ob.flags&0x40 != 0 { // V4L2_BUF_FLAG_ERROR
		return 0, errors.New("the block failed the picture")
	}
	if m.outMulti {
		return int(opl[0].bytesused), nil
	}
	return int(ob.bytesused), nil
}

func (m *m2m) close() {
	if m.in != nil {
		_ = unix.Munmap(m.in)
	}
	if m.out != nil {
		_ = unix.Munmap(m.out)
	}
	unix.Close(m.fd)
}

// Detector holds the two devices open for frames of one size.
type Detector struct {
	mu         sync.Mutex
	dip, fd    *m2m
	rawW, rawH int
	w, h       int
}

// New opens the blocks for raw frames of rawW x rawH.
func New(rawW, rawH int) (*Detector, error) {
	h := (rawH*detW/rawW + 1) &^ 1
	d := &Detector{rawW: rawW, rawH: rawH, w: detW, h: min(h, 480)}
	var err error
	if d.dip, err = open("mtk-dip"); err != nil {
		return nil, err
	}
	if err = d.dip.setFmt(bufTypeOutputMplane, uint32(rawW), uint32(rawH), pixPBAA); err == nil {
		err = d.dip.setFmt(bufTypeCaptureMplane, uint32(d.w), uint32(d.h), pixYUYV)
	}
	if err == nil {
		err = d.dip.buffers(bufTypeCaptureMplane)
	}
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("facedetect: the ISP: %w", err)
	}
	if d.fd, err = open("mtk-fd-4.0"); err != nil {
		d.Close()
		return nil, err
	}
	if err = d.fd.setFmt(bufTypeOutputMplane, uint32(d.w), uint32(d.h), pixYUYV); err == nil {
		err = d.pyramid()
	}
	if err == nil {
		err = d.fd.buffers(bufTypeMetaCapture)
	}
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("facedetect: the detector: %w", err)
	}
	return d, nil
}

// pyramid sets the sizes the detector scans at: the picture's, then 0.8 of it a step, down to its
// smallest (26 pixels), as MediaTek's camera stack does.
func (d *Detector) pyramid() error {
	var ws, hs [fdScales]uint16
	n := 0
	for s := 1.0; n < fdScales; s *= 0.8 {
		w, h := int(float64(d.w)*s)&^1, int(float64(d.h)*s)&^1
		if w < 26 || h < 26 {
			break
		}
		ws[n], hs[n] = uint16(w), uint16(h)
		n++
	}
	var cs [3]extControl
	put := func(c *extControl, id, size uint32, v uint64) {
		binary.LittleEndian.PutUint32(c[0:], id)
		binary.LittleEndian.PutUint32(c[4:], size)
		binary.LittleEndian.PutUint64(c[12:], v)
	}
	put(&cs[0], cidFDScaleWidth, uint32(unsafe.Sizeof(ws)), uint64(uintptr(unsafe.Pointer(&ws))))
	put(&cs[1], cidFDScaleHeight, uint32(unsafe.Sizeof(hs)), uint64(uintptr(unsafe.Pointer(&hs))))
	put(&cs[2], cidFDScaleNum, 0, uint64(n))
	ec := extControls{which: 0, count: 3, controls: uintptr(unsafe.Pointer(&cs[0]))}
	return ioctl(d.fd.fd, vidiocSExtCtrls, unsafe.Pointer(&ec))
}

// Size is the picture the detector sees: a face's Box is in it.
func (d *Detector) Size() (w, h int) { return d.w, d.h }

// Tone is how the ISP levels the frame (hardware/camera's ISPTone): the black level at 10 bits; the
// gains in 1/512.
type Tone struct{ Black, Red, Blue, Gain int }

// Detect develops raw (packed RAW10, bpl bytes a line, of the size New was given) and finds the faces
// in it, the likeliest first.
func (d *Detector) Detect(raw []byte, bpl int, t Tone) ([]Face, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dip == nil || d.fd == nil {
		return nil, errors.New("facedetect: closed")
	}
	if bpl*d.rawH > len(raw) || bpl*d.rawH > len(d.dip.in) {
		return nil, errors.New("facedetect: a frame of another size")
	}
	for _, c := range [][2]uint32{{cidBlackLevel, uint32(t.Black)}, {cidRedBalance, uint32(t.Red)},
		{cidBlueBalance, uint32(t.Blue)}, {cidDigitalGain, uint32(t.Gain)}} {
		if err := d.dip.ctrl(c[0], c[1]); err != nil {
			return nil, fmt.Errorf("facedetect: the ISP's leveling: %w", err)
		}
	}
	copy(d.dip.in, raw[:bpl*d.rawH])
	n, err := d.dip.run(bpl*d.rawH, bufTypeCaptureMplane)
	if err != nil {
		return nil, fmt.Errorf("facedetect: the ISP: %w", err)
	}
	copy(d.fd.in, d.dip.out[:min(n, len(d.fd.in))])
	if _, err := d.fd.run(d.w*d.h*2, bufTypeMetaCapture); err != nil {
		return nil, fmt.Errorf("facedetect: the detector: %w", err)
	}
	return merge(parse(d.fd.out)), nil
}

// parse reads the detector's records: each 16 bytes, little-endian bit fields (found on the unit:
// index 12 bits, type 1, then the box's corners x0, y0, x1, y1 at 10 bits each, then the score, 11).
func parse(out []byte) []Face {
	if len(out) < fdResultSize*fdMaxFaces+2 {
		return nil
	}
	n := int(binary.LittleEndian.Uint16(out[fdResultSize*fdMaxFaces:]))
	faces := make([]Face, 0, min(n, fdMaxFaces))
	for i := 0; i < n && i < fdMaxFaces; i++ {
		lo := binary.LittleEndian.Uint64(out[i*fdResultSize:])
		f := func(o, bits uint) int { return int(lo >> o & (1<<bits - 1)) }
		faces = append(faces, Face{Box: image.Rect(f(13, 10), f(23, 10), f(33, 10), f(43, 10)), Score: f(53, 11), Votes: 1})
	}
	return faces
}

// merge takes the candidates the detector gives for one face (at several scales and shifts, as a
// detector does) down to one each: a box that overlaps a likelier kept one by more than a third of
// their union is its vote.
func merge(c []Face) []Face {
	sort.Slice(c, func(i, j int) bool { return c[i].Score > c[j].Score })
	var kept []Face
next:
	for _, f := range c {
		for i := range kept {
			if iou(f.Box, kept[i].Box) > 1.0/3 {
				kept[i].Votes++
				continue next
			}
		}
		kept = append(kept, f)
	}
	return kept
}

func iou(a, b image.Rectangle) float64 {
	in := a.Intersect(b)
	if in.Empty() {
		return 0
	}
	ia := in.Dx() * in.Dy()
	return float64(ia) / float64(a.Dx()*a.Dy()+b.Dx()*b.Dy()-ia)
}

// Close lets the blocks go.
func (d *Detector) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dip != nil {
		d.dip.close()
		d.dip = nil
	}
	if d.fd != nil {
		d.fd.close()
		d.fd = nil
	}
}
