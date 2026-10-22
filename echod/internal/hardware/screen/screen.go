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
	alphaMask uint32  // 0xff when the framebuffer has a transparency channel, else 0

	canvas *image.RGBA

	// shadow is what each page of the framebuffer already holds, in RAM, so a frame writes only the
	// rows that differ: writing the framebuffer's own memory is slow (on the Spot most of a frame),
	// and most frames change a few rows. row is scratch for one converted row.
	shadow [][]byte
	row    []byte
	bands  [][]byte // scratch for rotate: one panel row per canvas column in a band

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
	// A framebuffer without a transparency channel (mainline's DRM fbdev: XRGB8888, transp offset 0,
	// length 0) must not receive the alpha byte at all: shifted by 0 it lands on the blue byte and
	// every pixel comes out with full blue added. Amazon's mtkfb put alpha at bit 24, which hid this.
	if d.v.Transp[1] > 0 {
		d.alphaMask = 0xff
	}

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
func (d *Device) Present() error {
	if d.tintTable != nil {
		drawn := d.canvas
		d.canvas = d.tinted()
		defer func() { d.canvas = drawn }()
	}
	next := d.page
	if d.pages > 1 {
		next = (d.page + 1) % d.pages
	}
	dst := d.mem[next*d.pageBytes : (next+1)*d.pageBytes]

	img := d.source()
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
				pixel := uint32(img.Pix[i])<<sr | uint32(img.Pix[i+1])<<sg | uint32(img.Pix[i+2])<<sb | (uint32(img.Pix[i+3])&d.alphaMask)<<sa
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
	if len(d.bands) != band || len(d.bands[0]) != rowBytes {
		d.bands = make([][]byte, band)
		for k := range d.bands {
			d.bands[k] = make([]byte, rowBytes)
		}
	}
	rows := d.bands
	whole := d.takeStale(next)
	cols := min(w, d.panelH)
	height := min(h, d.panelW)

	for x0 := 0; x0 < cols; x0 += band {
		n := min(band, cols-x0)
		// One loop per layout, so the choice is made once a band and not once a pixel.
		switch {
		case fast && swap:
			for y := 0; y < height; y++ {
				src := img.Pix[y*img.Stride+x0*4 : y*img.Stride+(x0+n)*4]
				px := (d.panelW - 1 - y) * 4
				for k := 0; k < n; k++ {
					p := src[k*4 : k*4+4 : k*4+4]
					binary.LittleEndian.PutUint32(rows[k][px:px+4], uint32(p[2])|uint32(p[1])<<8|uint32(p[0])<<16|uint32(p[3])<<24)
				}
			}
		case fast:
			for y := 0; y < height; y++ {
				src := img.Pix[y*img.Stride+x0*4 : y*img.Stride+(x0+n)*4]
				px := (d.panelW - 1 - y) * 4
				for k := 0; k < n; k++ {
					copy(rows[k][px:px+4], src[k*4:k*4+4])
				}
			}
		default:
			for y := 0; y < height; y++ {
				src := img.Pix[y*img.Stride+x0*4 : y*img.Stride+(x0+n)*4]
				px := (d.panelW - 1 - y) * 4
				for k := 0; k < n; k++ {
					p := src[k*4 : k*4+4 : k*4+4]
					binary.LittleEndian.PutUint32(rows[k][px:px+4], uint32(p[0])<<sr|uint32(p[1])<<sg|uint32(p[2])<<sb|(uint32(p[3])&d.alphaMask)<<sa)
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

// pan shows page next. The mainline kernel's simple-framebuffer on the 2nd gen Show 8 has one page
// and no pan operation (FBIOPAN_DISPLAY gives EINVAL): there it scans out what was written. DRM's
// fbdev emulation (mediatek-drm) has two pages and pans with an atomic commit, at the next vblank.
// On the Echo Show 5's mainline kernel that emulation (the framebuffer without a transparency
// channel) has a single page, where a pan to the page on show is only a wasted commit.
func (d *Device) pan(next int) error {
	if d.pages <= 1 && (layout.AthenaMainline() || d.v.Transp[1] == 0) {
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
	if err := os.WriteFile(path, []byte(strconv.Itoa(raw)), 0o644); err != nil {
		return fmt.Errorf("screen: backlight: %w", err)
	}
	return nil
}

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
