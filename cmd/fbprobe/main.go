//go:build linux

// fbprobe draws straight to the Echo Show 5's framebuffer: a background, colour bars and large
// text, rotated for the landscape orientation the device is used in. It is the first step of the
// TECHO5 display layer — proving that the kernel framebuffer reaches the panel without Android.
//
//	fbprobe                 # paint the test image and pan it onto the panel
//	fbprobe -fill 1c1511    # solid colour only
//	fbprobe -info           # print the framebuffer geometry and exit
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"golang.org/x/sys/unix"
)

const (
	fbDev = "/dev/graphics/fb0"

	fbioGetVScreenInfo = 0x4600
	fbioPutVScreenInfo = 0x4601
	fbioGetFScreenInfo = 0x4602
	fbioPanDisplay     = 0x4606
	fbioBlank          = 0x4611
)

// fb_var_screeninfo, 160 bytes on every ABI (all u32).
type varInfo struct {
	Xres, Yres, XresVirtual, YresVirtual, Xoffset, Yoffset uint32
	BitsPerPixel, Grayscale                               uint32
	Red, Green, Blue, Transp                              [3]uint32 // offset, length, msb_right
	Nonstd, Activate, Height, Width, AccelFlags           uint32
	Pixclock, LeftMargin, RightMargin, UpperMargin        uint32
	LowerMargin, HsyncLen, VsyncLen, Sync, Vmode, Rotate  uint32
	Colorspace                                            uint32
	Reserved                                              [4]uint32
}

// fb_fix_screeninfo on a 32-bit kernel ABI... this kernel is arm64 with a 32-bit userspace, so the
// compat layout applies: unsigned long is 4 bytes here.
type fixInfo struct {
	ID                        [16]byte
	SmemStart                 uint32
	SmemLen                   uint32
	Type, TypeAux, Visual     uint32
	Xpanstep, Ypanstep        uint16
	Ywrapstep                 uint16
	_                         uint16
	LineLength                uint32
	MmioStart                 uint32
	MmioLen, Accel            uint32
	Capabilities              uint16
	_                         [2]uint16
	_                         uint16
}

func ioctl(fd uintptr, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

func main() {
	info := flag.Bool("info", false, "print framebuffer geometry and exit")
	fill := flag.String("fill", "", "fill with this rrggbb colour only")
	hold := flag.Duration("hold", 0, "keep repainting for this long (0 = paint once and exit)")
	mapSize := flag.Int("map", 0, "try this mmap size first, in bytes")
	flag.Parse()

	f, err := os.OpenFile(fbDev, os.O_RDWR, 0)
	if err != nil {
		fatal("open %s: %v", fbDev, err)
	}
	defer f.Close()

	var v varInfo
	var fx fixInfo
	if err := ioctl(f.Fd(), fbioGetVScreenInfo, unsafe.Pointer(&v)); err != nil {
		fatal("FBIOGET_VSCREENINFO: %v", err)
	}
	if err := ioctl(f.Fd(), fbioGetFScreenInfo, unsafe.Pointer(&fx)); err != nil {
		fatal("FBIOGET_FSCREENINFO: %v", err)
	}
	fmt.Printf("fb: %dx%d (virtual %dx%d) %d bpp, line %d bytes, smem %d bytes, offsets r%d g%d b%d a%d, rotate %d\n",
		v.Xres, v.Yres, v.XresVirtual, v.YresVirtual, v.BitsPerPixel, fx.LineLength, fx.SmemLen,
		v.Red[0], v.Green[0], v.Blue[0], v.Transp[0], v.Rotate)
	if *info {
		return
	}

	// The MediaTek driver reports smem_len as 0; the virtual geometry is what it maps. If the full
	// mapping is refused, try one frame, then a page, to learn what the driver will give.
	full := int(fx.LineLength) * int(v.YresVirtual)
	if fx.SmemLen != 0 && full > int(fx.SmemLen) {
		full = int(fx.SmemLen)
	}
	frame1 := int(fx.LineLength) * int(v.Yres)
	var mem []byte
	for _, size := range []int{*mapSize, full, frame1, 4096} {
		if size <= 0 {
			continue
		}
		m, err := unix.Mmap(int(f.Fd()), 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mmap %d bytes: %v\n", size, err)
			continue
		}
		fmt.Printf("mapped %d bytes\n", size)
		mem = m
		break
	}
	if mem == nil {
		fatal("no mapping size accepted")
	}
	defer unix.Munmap(mem)
	if len(mem) < frame1 {
		fatal("mapping too small to hold a frame (%d < %d)", len(mem), frame1)
	}

	// The panel is portrait (480 wide, 960 tall); the device is used landscape. Compose a 960x480
	// landscape image and rotate it 90 degrees clockwise onto the panel.
	w, h := int(v.Yres), int(v.Xres) // landscape canvas
	img := image.NewRGBA(image.Rect(0, 0, w, h))

	bg := color.RGBA{0x1c, 0x15, 0x11, 0xff}
	if *fill != "" {
		if c, err := strconv.ParseUint(*fill, 16, 32); err == nil {
			bg = color.RGBA{uint8(c >> 16), uint8(c >> 8), uint8(c), 0xff}
		}
	}
	draw.Draw(img, img.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)

	if *fill == "" {
		// Colour bars along the bottom, an amber frame, and text.
		bars := []color.RGBA{{0xff, 0, 0, 0xff}, {0, 0xff, 0, 0xff}, {0, 0, 0xff, 0xff}, {0xff, 0xff, 0xff, 0xff}, {0xe9, 0xa2, 0x3b, 0xff}}
		bw := w / len(bars)
		for i, c := range bars {
			draw.Draw(img, image.Rect(i*bw, h-60, (i+1)*bw, h), image.NewUniform(c), image.Point{}, draw.Src)
		}
		amber := color.RGBA{0xe9, 0xa2, 0x3b, 0xff}
		frame(img, img.Bounds().Inset(8), 4, amber)
		text(img, "TECHO5", 40, 120, 8, amber)
		text(img, "framebuffer 960x480", 40, 220, 3, color.RGBA{0xe8, 0xdc, 0xc8, 0xff})
		text(img, time.Now().Format("15:04:05"), 40, 300, 6, color.RGBA{0xe8, 0xdc, 0xc8, 0xff})
	}

	paint := func() {
		blit(mem, img, int(fx.LineLength), int(v.Xres), int(v.Yres), v)
		v.Xoffset, v.Yoffset = 0, 0
		if err := ioctl(f.Fd(), fbioPanDisplay, unsafe.Pointer(&v)); err != nil {
			fmt.Fprintf(os.Stderr, "FBIOPAN_DISPLAY: %v\n", err)
		}
	}
	paint()
	fmt.Println("painted")
	if *hold > 0 {
		deadline := time.Now().Add(*hold)
		for time.Now().Before(deadline) {
			time.Sleep(time.Second)
			if *fill == "" {
				draw.Draw(img, image.Rect(40, 250, w-40, 360), image.NewUniform(bg), image.Point{}, draw.Src)
				text(img, time.Now().Format("15:04:05"), 40, 300, 6, color.RGBA{0xe8, 0xdc, 0xc8, 0xff})
			}
			paint()
		}
	}
}

// blit writes the landscape image onto the portrait framebuffer, rotated 90 degrees clockwise:
// landscape (x, y) lands at panel (panelW-1-y, x). Pixel order follows the var info's offsets.
func blit(mem []byte, img *image.RGBA, line, panelW, panelH int, v varInfo) {
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			i := img.PixOffset(x, y)
			r, g, b, a := img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3]
			px, py := panelW-1-y, x
			if px < 0 || px >= panelW || py < 0 || py >= panelH {
				continue
			}
			off := py*line + px*4
			var pixel uint32
			pixel |= uint32(r) << v.Red[0]
			pixel |= uint32(g) << v.Green[0]
			pixel |= uint32(b) << v.Blue[0]
			if v.Transp[1] > 0 { // no transparency channel (DRM fbdev's XRGB8888): alpha would land on blue
				pixel |= uint32(a) << v.Transp[0]
			}
			binary.LittleEndian.PutUint32(mem[off:], pixel)
		}
	}
}

func frame(img *image.RGBA, r image.Rectangle, t int, c color.RGBA) {
	u := image.NewUniform(c)
	draw.Draw(img, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+t), u, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(r.Min.X, r.Max.Y-t, r.Max.X, r.Max.Y), u, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(r.Min.X, r.Min.Y, r.Min.X+t, r.Max.Y), u, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(r.Max.X-t, r.Min.Y, r.Max.X, r.Max.Y), u, image.Point{}, draw.Src)
}

// text draws with the built-in 7x13 face, scaled up by an integer factor. Crude, and enough to
// read from across a desk; a real face comes with the display layer proper.
func text(img *image.RGBA, s string, x, y, scale int, c color.RGBA) {
	small := image.NewRGBA(image.Rect(0, 0, 7*len(s)+2, 14))
	d := &font.Drawer{Dst: small, Src: image.NewUniform(c), Face: basicfont.Face7x13, Dot: fixed.P(1, 11)}
	d.DrawString(s)
	for sy := 0; sy < small.Rect.Dy(); sy++ {
		for sx := 0; sx < small.Rect.Dx(); sx++ {
			if small.Pix[small.PixOffset(sx, sy)+3] == 0 {
				continue
			}
			draw.Draw(img, image.Rect(x+sx*scale, y+sy*scale, x+(sx+1)*scale, y+(sy+1)*scale), image.NewUniform(c), image.Point{}, draw.Src)
		}
	}
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "fbprobe: "+format+"\n", a...)
	os.Exit(1)
}
