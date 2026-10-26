//go:build !dot && !panel

// Package screen owns the panel of a device with a screen (the Echo Show 5, the Echo Spot): the kernel framebuffer it is painted through and the
// backlight that lights it.
//
// On the Show the panel is portrait, 480 wide and 960 tall, and the device sits landscape, so the canvas
// anyone draws on is 960×480 and Present rotates it onto the panel (rotated, geometry_cronos.go). The
// Spot's round panel is 480×480 and drawn as it is. The framebuffer has room for several
// pages; each Present paints the page not on show and pans to it, so a frame is never seen half
// drawn. Nothing here decides what is on the screen — that is the display feature's.
//
// Why fbdev and not the vendor's display manager or DRM: docs/hardware.md (Display). The LineageOS
// kernel frees this buffer only once Android's compositor takes over, which a Linux boot never does.
package screen

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

const (
	// Two names for the one node: the initramfs links the Android one to the plain one.
	devPath = "/dev/graphics/fb0"
	altPath = "/dev/fb0"

	backlightPath = "/sys/class/leds/lcd-backlight/brightness"

	// BacklightMax is the driver's top; the bootloader leaves the panel at 90.
	BacklightMax = 255

	fbioGetVScreenInfo = 0x4600
	fbioGetFScreenInfo = 0x4602
	fbioPanDisplay     = 0x4606
	fbioBlank          = 0x4611 // the argument is the level itself; 0 is FB_BLANK_UNBLANK
)

// fb_var_screeninfo: 160 bytes on every ABI, all u32.
type varInfo struct {
	Xres, Yres, XresVirtual, YresVirtual, Xoffset, Yoffset uint32
	BitsPerPixel, Grayscale                                uint32
	Red, Green, Blue, Transp                               [3]uint32 // offset, length, msb_right
	Nonstd, Activate, Height, Width, AccelFlags            uint32
	Pixclock, LeftMargin, RightMargin, UpperMargin         uint32
	LowerMargin, HsyncLen, VsyncLen, Sync, Vmode, Rotate   uint32
	Colorspace                                             uint32
	Reserved                                               [4]uint32
}

// fb_fix_screeninfo. Its two addresses are the kernel's unsigned long, which is Go's uint on both
// ABIs, so the struct is 68 bytes on 32-bit and 80 on arm64. FBIOGET_FSCREENINFO does not encode a
// size: the kernel copies its own, and a struct too small is overwritten past its end.
type fixInfo struct {
	ID                    [16]byte
	SmemStart             uint
	SmemLen               uint32
	Type, TypeAux, Visual uint32
	Xpanstep, Ypanstep    uint16
	Ywrapstep             uint16
	_                     uint16
	LineLength            uint32
	MmioStart             uint
	MmioLen, Accel        uint32
	Capabilities          uint16
	_                     [2]uint16
	_                     uint16
}

func ioctl(fd uintptr, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

// Device is the open framebuffer.
type Device struct {
	f   *os.File
	mem []byte
	v   varInfo

	line      int // bytes per panel row
	pageBytes int
	pages     int
	page      int // the page on show
	panelW    int
	panelH    int
	shift     [4]uint // where red, green, blue and alpha sit in a pixel

	canvas *image.RGBA

	// shadow is what each page of the framebuffer already holds, in RAM, so a frame writes only the
	// rows that differ: writing the framebuffer's own memory is slow (on the Spot most of a frame),
	// and most frames change a few rows. row is scratch for one converted row.
	shadow [][]byte
	row    []byte
	bands  [][]byte // scratch for rotate: one panel row per canvas column in a band

	// staged is the frame being turned onto the panel while the next is drawn (Present), pending
	// closed when it is done, and pendErr whether it failed.
	staged  *image.RGBA
	pending chan struct{}
	pendErr error

	// stale marks a page that a frame (PresentFrame) was painted on, so it no longer holds what its
	// shadow says: the next Present onto it writes every row.
	stale []bool

	// tint is what the panel is shown with (tint.go); tintTable applies it to a copy of each frame,
	// tintImg.
	tint      Tint
	tintTable *[3][256]uint8
	tintImg   *image.RGBA

	// filter is laid over every row on its way to the panel (SetFilter, filter.go).
	filter rowFilter
}

// SetFilter lays f over every row of the canvas on its way to the panel, a row of four-byte RGBA
// pixels at a time, y its row; nil takes it off. name tells one filter from another, so a new one
// filters every row again. The canvas itself is not touched: drawing goes on as before, and a row
// drawn again is filtered again from what was drawn, never from what was filtered.
func (d *Device) SetFilter(name string, f func(row []byte, y int)) { d.filter.set(name, f) }

// source is what Present sends: the canvas, or the canvas through the filter.
func (d *Device) source() *image.RGBA { return d.filter.over(d.canvas) }

// Open maps the framebuffer and reads its geometry.
func Open() (*Device, error) {
	f, err := os.OpenFile(devPath, os.O_RDWR, 0)
	if err != nil {
		if f, err = os.OpenFile(altPath, os.O_RDWR, 0); err != nil {
			return nil, fmt.Errorf("screen: %w", err)
		}
	}

	d := &Device{f: f}
	var fx fixInfo
	if err := ioctl(f.Fd(), fbioGetVScreenInfo, unsafe.Pointer(&d.v)); err != nil {
		f.Close()
		return nil, fmt.Errorf("screen: FBIOGET_VSCREENINFO: %w", err)
	}
	if err := ioctl(f.Fd(), fbioGetFScreenInfo, unsafe.Pointer(&fx)); err != nil {
		f.Close()
		return nil, fmt.Errorf("screen: FBIOGET_FSCREENINFO: %w", err)
	}
	if d.v.BitsPerPixel != 32 {
		f.Close()
		return nil, fmt.Errorf("screen: %d bits per pixel, want 32", d.v.BitsPerPixel)
	}

	d.panelW, d.panelH = int(d.v.Xres), int(d.v.Yres)
	d.line = int(fx.LineLength)
	if d.line == 0 {
		d.line = int(d.v.XresVirtual) * 4
	}
	d.pageBytes = d.line * d.panelH
	d.pages = max(int(d.v.YresVirtual)/max(d.panelH, 1), 1)
	if fx.SmemLen != 0 && int(fx.SmemLen) < d.pageBytes*d.pages {
		d.pages = max(int(fx.SmemLen)/d.pageBytes, 1)
	}
	d.shift = pixelShift(&d.v)

	// On TECHO5's mainline kernel /dev/fb0 can be DRM's fbdev emulation (mediatek-drm), which sets no
	// mode of its own while there is no console on it: unblanking does, so the panel shows this buffer
	// even before the first pan. simple-framebuffer has no blank operation and says EINVAL; that is fine.
	// The Skylight's rockchip-drm fbdev emulation is the same kind.
	if layout.AthenaMainline() || layout.Skylight() {
		_ = ioctl(f.Fd(), fbioBlank, nil)
	}

	// The driver reports what it will map through the virtual geometry; if the whole of it is
	// refused, a single page still gives a screen, just one that can tear.
	for d.pages >= 1 {
		mem, err := unix.Mmap(int(f.Fd()), 0, d.pageBytes*d.pages, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
		if err == nil {
			d.mem = mem
			break
		}
		if d.pages == 1 {
			f.Close()
			return nil, fmt.Errorf("screen: mmap %d bytes: %w", d.pageBytes, err)
		}
		d.pages--
	}
	d.page = int(d.v.Yoffset) / max(d.panelH, 1)
	if d.page >= d.pages {
		d.page = 0
	}

	// Landscape canvas: the panel turned a quarter turn, where the device needs it.
	if rotated {
		d.canvas = image.NewRGBA(image.Rect(0, 0, d.panelH, d.panelW))
	} else {
		d.canvas = image.NewRGBA(image.Rect(0, 0, d.panelW, d.panelH))
	}
	return d, nil
}

// pixelShift is where red, green, blue and alpha sit in a pixel. A layout with no alpha field (DRM's
// fbdev emulation gives XRGB8888: transparency length 0, offset 0) has its spare byte at 24, and
// alpha goes there, where the display ignores it, rather than at 0, on top of blue.
func pixelShift(v *varInfo) [4]uint {
	s := [4]uint{uint(v.Red[0]), uint(v.Green[0]), uint(v.Blue[0]), uint(v.Transp[0])}
	if v.Transp[1] == 0 && s[0] < 24 && s[1] < 24 && s[2] < 24 {
		s[3] = 24
	}
	return s
}

// Size is the canvas: 960×480 on the Show, 480×480 on the Spot.
func (d *Device) Size() (w, h int) { return d.canvas.Rect.Dx(), d.canvas.Rect.Dy() }

// Pages is how many frames the buffer holds; more than one means Present does not tear.
func (d *Device) Pages() int { return d.pages }

// Canvas is what to draw on. Present shows it.
func (d *Device) Canvas() *image.RGBA { return d.canvas }

// String describes the framebuffer, for the log.
func (d *Device) String() string {
	return fmt.Sprintf("%dx%d panel, %d pages, %d bytes/line, rgba at %d/%d/%d/%d",
		d.panelW, d.panelH, d.pages, d.line, d.shift[0], d.shift[1], d.shift[2], d.shift[3])
}

// Present paints the canvas onto the page not on show, rotated onto the portrait panel, and pans to
// it. Landscape (x, y) lands at panel (panelW-1-y, x), so each landscape column becomes one panel
// row, written contiguously.
//
// On a rotated panel the frame is copied as it is and turned onto the page while the next frame is
// drawn: the turn is a third of a frame on the Show 8, and a copy a tenth of it. The next Present (or
// PresentFrame, or Close) waits for it first, and says if it failed.
func (d *Device) Present() error {
	err := d.settle()
	// The tint is laid on here, on the caller's goroutine: the canvas is put back before the next frame
	// is drawn on it, and the background turn below works on the staged copy.
	if d.tintTable != nil {
		drawn := d.canvas
		d.canvas = d.tinted()
		defer func() { d.canvas = drawn }()
	}
	img := d.source()
	if !rotated {
		if e := d.present(img); e != nil {
			return e
		}
		return err
	}
	if d.staged == nil || d.staged.Rect != img.Rect {
		d.staged = image.NewRGBA(img.Rect)
	}
	staged := d.staged
	for y := range img.Rect.Dy() {
		copy(staged.Pix[y*staged.Stride:(y+1)*staged.Stride], img.Pix[y*img.Stride:y*img.Stride+staged.Stride])
	}
	done := make(chan struct{})
	d.pending = done
	go func() {
		d.pendErr = d.present(staged)
		close(done)
	}()
	return err
}

// Settle waits for the last Present to reach the panel, and says if it failed: for whatever has to
// happen only once the frame is showing, such as lighting the backlight over it.
func (d *Device) Settle() error { return d.settle() }

// settle waits for the frame being put on the panel, if one is, and says if it failed.
func (d *Device) settle() error {
	if d.pending == nil {
		return nil
	}
	<-d.pending
	err := d.pendErr
	d.pending, d.pendErr = nil, nil
	return err
}

// present puts img on the page not on show, and shows it.
func (d *Device) present(img *image.RGBA) error {
	next := d.page
	if d.pages > 1 {
		next = (d.page + 1) % d.pages
	}
	dst := d.mem[next*d.pageBytes : (next+1)*d.pageBytes]

	w, h := img.Rect.Dx(), img.Rect.Dy()
	sr, sg, sb, sa := d.shift[0], d.shift[1], d.shift[2], d.shift[3]
	// The common layouts row by row, a copy and at most a swap of red and blue, rather than a pixel
	// packed at a time: on the Spot this is most of a frame's cost otherwise.
	if !rotated && w <= d.panelW && sg == 8 && sa == 24 && ((sr == 0 && sb == 16) || (sr == 16 && sb == 0)) {
		swap := sr == 16
		if d.shadow == nil {
			d.shadow = make([][]byte, max(d.pages, 1))
			d.row = make([]byte, w*4)
		}
		shadow := d.shadow[next]
		if shadow == nil {
			// A page never written by this process: take what it holds, so the first frame compares
			// against the truth rather than against zeros.
			shadow = append([]byte(nil), dst...)
			d.shadow[next] = shadow
		}
		row := d.row[:w*4]
		whole := d.takeStale(next)
		for y := 0; y < h && y < d.panelH; y++ {
			copy(row, img.Pix[y*img.Stride:y*img.Stride+w*4])
			if swap {
				for i := 0; i+3 < len(row); i += 4 {
					row[i], row[i+2] = row[i+2], row[i]
				}
			}
			at := y * d.line
			if !whole && bytes.Equal(row, shadow[at:at+w*4]) {
				continue
			}
			copy(shadow[at:at+w*4], row)
			copy(dst[at:at+w*4], row)
		}
		return d.pan(next)
	}
	if !rotated {
		d.takeStale(next)
		for y := 0; y < h && y < d.panelH; y++ {
			row := dst[y*d.line : y*d.line+d.panelW*4]
			for x := 0; x < w && x < d.panelW; x++ {
				i := y*img.Stride + x*4
				pixel := uint32(img.Pix[i])<<sr | uint32(img.Pix[i+1])<<sg | uint32(img.Pix[i+2])<<sb | uint32(img.Pix[i+3])<<sa
				binary.LittleEndian.PutUint32(row[x*4:x*4+4], pixel)
			}
		}
		return d.pan(next)
	}
	d.rotate(img, dst, next)
	return d.pan(next)
}

// band is how many canvas columns rotate turns at once. A column of the canvas is a row of the panel,
// and reading one column alone touches a different cache line for every pixel; eight side by side
// read 32 bytes from each line instead of 4.
const band = 8

// rotate paints the canvas onto page next of the rotated panel. Each panel row is built in RAM and
// compared with what the page already holds, and only a row that differs is written: the
// framebuffer's own memory is slow to write, and most frames change few of its rows. A row is
// written whole, in order, which that memory is quickest at.
func (d *Device) rotate(img *image.RGBA, dst []byte, next int) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	sr, sg, sb, sa := d.shift[0], d.shift[1], d.shift[2], d.shift[3]
	// The common layouts are a byte copy, with red and blue swapped or not; anything else is packed
	// a pixel at a time.
	fast := sg == 8 && sa == 24 && ((sr == 0 && sb == 16) || (sr == 16 && sb == 0))
	swap := sr == 16

	if d.shadow == nil {
		d.shadow = make([][]byte, max(d.pages, 1))
	}
	shadow := d.shadow[next]
	if shadow == nil {
		shadow = append([]byte(nil), dst...)
		d.shadow[next] = shadow
	}
	rowBytes := d.panelW * 4
	if len(d.bands) != rotateWorkers*band || len(d.bands[0]) != rowBytes {
		d.bands = make([][]byte, rotateWorkers*band)
		for k := range d.bands {
			d.bands[k] = make([]byte, rowBytes)
		}
	}
	whole := d.takeStale(next)
	cols := min(w, d.panelH)
	height := min(h, d.panelW)

	// The panel's rows split between rotateWorkers goroutines, each with its own scratch: they write
	// rows of their own of the page and its shadow.
	var wg sync.WaitGroup
	per := ((cols+band-1)/band + rotateWorkers - 1) / rotateWorkers * band
	for i := 1; i < rotateWorkers && i*per < cols; i++ {
		wg.Add(1)
		go func(lo, hi int, rows [][]byte) {
			defer wg.Done()
			d.rotateRows(img, dst, shadow, rows, lo, hi, height, fast, swap, whole)
		}(i*per, min((i+1)*per, cols), d.bands[i*band:(i+1)*band])
	}
	d.rotateRows(img, dst, shadow, d.bands[:band], 0, min(per, cols), height, fast, swap, whole)
	wg.Wait()
}

// rotateWorkers is how many goroutines rotate is split over: two, for the Show's cores, which come
// and go with the load. More are no quicker: by then the memory is what it waits on.
const rotateWorkers = 2

// rotateRows is rotate's work for the panel's rows lo to hi (the canvas's columns), with rows of
// scratch, band of them.
func (d *Device) rotateRows(img *image.RGBA, dst, shadow []byte, rows [][]byte, lo, hi, height int, fast, swap, whole bool) {
	sr, sg, sb, sa := d.shift[0], d.shift[1], d.shift[2], d.shift[3]
	rowBytes := d.panelW * 4
	for x0 := lo; x0 < hi; x0 += band {
		n := min(band, hi-x0)
		// One loop per layout, so the choice is made once a band and not once a pixel.
		switch {
		case fast:
			// A word to a pixel, read and written whole: on the Show's processor a pixel's bytes one at a
			// time are four loads and four stores, and this loop runs a million times a frame. Eight
			// panel rows filled at once, unrolled, so no row's bounds are checked a pixel at a time.
			pix := words(img.Pix)
			stride := img.Stride / 4
			if n < band || height > d.panelW {
				for y := 0; y < height; y++ {
					src := pix[y*stride+x0 : y*stride+x0+n]
					px := (d.panelW - 1 - y) * 4
					for k, v := range src {
						if swap {
							v = v&0xff00ff00 | v&0xff<<16 | v>>16&0xff
						}
						binary.LittleEndian.PutUint32(rows[k][px:px+4], v)
					}
				}
				break
			}
			r0, r1, r2, r3 := words(rows[0])[:d.panelW], words(rows[1])[:d.panelW], words(rows[2])[:d.panelW], words(rows[3])[:d.panelW]
			r4, r5, r6, r7 := words(rows[4])[:d.panelW], words(rows[5])[:d.panelW], words(rows[6])[:d.panelW], words(rows[7])[:d.panelW]
			for y := 0; y < height; y++ {
				s := pix[y*stride+x0 : y*stride+x0+band : y*stride+x0+band]
				px := d.panelW - 1 - y
				v0, v1, v2, v3, v4, v5, v6, v7 := s[0], s[1], s[2], s[3], s[4], s[5], s[6], s[7]
				if swap {
					v0 = v0&0xff00ff00 | v0&0xff<<16 | v0>>16&0xff
					v1 = v1&0xff00ff00 | v1&0xff<<16 | v1>>16&0xff
					v2 = v2&0xff00ff00 | v2&0xff<<16 | v2>>16&0xff
					v3 = v3&0xff00ff00 | v3&0xff<<16 | v3>>16&0xff
					v4 = v4&0xff00ff00 | v4&0xff<<16 | v4>>16&0xff
					v5 = v5&0xff00ff00 | v5&0xff<<16 | v5>>16&0xff
					v6 = v6&0xff00ff00 | v6&0xff<<16 | v6>>16&0xff
					v7 = v7&0xff00ff00 | v7&0xff<<16 | v7>>16&0xff
				}
				r0[px], r1[px], r2[px], r3[px], r4[px], r5[px], r6[px], r7[px] = v0, v1, v2, v3, v4, v5, v6, v7
			}
		default:
			for y := 0; y < height; y++ {
				src := img.Pix[y*img.Stride+x0*4 : y*img.Stride+(x0+n)*4]
				px := (d.panelW - 1 - y) * 4
				for k := 0; k < n; k++ {
					p := src[k*4 : k*4+4 : k*4+4]
					binary.LittleEndian.PutUint32(rows[k][px:px+4], uint32(p[0])<<sr|uint32(p[1])<<sg|uint32(p[2])<<sb|uint32(p[3])<<sa)
				}
			}
		}
		for k := 0; k < n; k++ {
			at := (x0 + k) * d.line
			row := rows[k][:rowBytes]
			if !whole && bytes.Equal(row, shadow[at:at+rowBytes]) {
				continue
			}
			copy(shadow[at:at+rowBytes], row)
			copy(dst[at:at+rowBytes], row)
		}
	}
}

// words is b's bytes a little-endian word each. b starts on a word, as the canvas's pixels and
// rotate's scratch rows do.
func words(b []byte) []uint32 {
	if len(b) < 4 {
		return nil
	}
	return unsafe.Slice((*uint32)(unsafe.Pointer(&b[0])), len(b)/4)
}

// pan shows page next. The mainline kernel's simple-framebuffer on the 2nd gen Show 8 has one page
// and no pan operation (FBIOPAN_DISPLAY gives EINVAL): there it scans out what was written. DRM's
// fbdev emulation (mediatek-drm) has two pages and pans with an atomic commit, at the next vblank.
// On the Echo Show 5's mainline kernel that emulation has a single page, where a pan to the page on
// show is only a wasted commit.
func (d *Device) pan(next int) error {
	if d.pages <= 1 && (layout.AthenaMainline() || layout.CronosMainline()) {
		d.page = 0
		return nil
	}
	v := d.v
	v.Xoffset = 0
	v.Yoffset = uint32(next * d.panelH)
	if err := ioctl(d.f.Fd(), fbioPanDisplay, unsafe.Pointer(&v)); err != nil {
		return fmt.Errorf("screen: FBIOPAN_DISPLAY: %w", err)
	}
	d.page = next
	d.v.Yoffset = v.Yoffset
	return nil
}

// Close unmaps and closes the framebuffer. The last frame stays on the panel.
func (d *Device) Close() error {
	_ = d.settle()
	if d.mem != nil {
		_ = unix.Munmap(d.mem)
		d.mem = nil
	}
	return d.f.Close()
}

// The 2nd gen Show 8 on TECHO5's mainline kernel has a pwm-backlight instead of Amazon's LED class
// node, with 1024 steps (the MT8183 display PWM). Callers keep the 0..BacklightMax scale; the
// level is spread over the driver's range here.
const (
	mainlineBacklightPath = "/sys/class/backlight/backlight-lcd0/brightness"
	mainlineBacklightMax  = 1023
)

// The Skylight's pwm-backlight has as many steps as its device tree's interpolated level table
// gives (235 for brightness-levels 20..255): the top is read from the driver, not assumed.
const (
	skylightBacklightDir = "/sys/class/backlight/backlight"
	skylightBacklightMax = 235
)

// The Echo Show 5 on TECHO5's mainline kernel has no LED class node either: its display PWM is a
// backlight class device whose range is read from max_brightness.
const cronosBacklightDir = "/sys/class/backlight/display-backlight"

func backlightNode() (path string, top int) {
	if layout.AthenaMainline() {
		return mainlineBacklightPath, mainlineBacklightMax
	}
	if layout.Skylight() {
		return skylightBacklightDir + "/brightness", readTop(skylightBacklightDir+"/max_brightness", skylightBacklightMax)
	}
	if _, err := os.Stat(backlightPath); os.IsNotExist(err) {
		if top := readTop(cronosBacklightDir+"/max_brightness", 0); top > 0 {
			return cronosBacklightDir + "/brightness", top
		}
	}
	return backlightPath, BacklightMax
}

// readTop is a backlight's max_brightness, or fallback where it cannot be read or is not positive.
func readTop(path string, fallback int) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return fallback
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

// SetBacklight sets the panel's backlight, 0 (off) to BacklightMax.
func SetBacklight(level int) error {
	level = min(max(level, 0), BacklightMax)
	path, top := backlightNode()
	raw := level * top / BacklightMax
	backlightMu.Lock()
	defer backlightMu.Unlock()
	if raw > 0 && backlightOff && layout.AthenaMainline() {
		// Some 2nd gen Show 8 panels (JD9365, #253) stop following the level once it has been 0, until
		// the PWM is driven full once; so a backlight coming back from off goes full first, for less
		// than a millisecond, before the level it is set to.
		if err := os.WriteFile(path, []byte(strconv.Itoa(top)), 0o644); err != nil {
			return fmt.Errorf("screen: backlight: %w", err)
		}
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(raw)), 0o644); err != nil {
		return fmt.Errorf("screen: backlight: %w", err)
	}
	backlightOff = raw == 0
	return nil
}

// backlightOff is whether the last level written was 0: until a level is written the backlight is taken
// as off, so the first one written after a start also goes full first.
var (
	backlightMu  sync.Mutex
	backlightOff = true
)

// Backlight reads the level the driver holds. The bootloader's own setting reads as 0 until
// something writes one.
func Backlight() (int, error) {
	path, top := backlightNode()
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("screen: backlight: %w", err)
	}
	raw, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, err
	}
	return (raw*BacklightMax + top/2) / top, nil
}
