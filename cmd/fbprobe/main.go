//go:build linux

// fbprobe draws straight to the Echo Show 5's framebuffer: a background, color bars and large
// text, rotated for the landscape orientation the device is used in. It is the first step of the
// TECHO5 display layer — proving that the kernel framebuffer reaches the panel without Android.
//
// It is also the only thing that can write to the screen in the rescue environment, where the
// daemon may not be running at all, so it can be given something to say instead of the test image.
//
//	fbprobe                 # paint the test image and pan it onto the panel
//	fbprobe -fill 1c1511    # solid color only
//	fbprobe -info           # print the framebuffer geometry and exit
//	fbprobe -title RESCUE -lines "first line|second line" -hold 1000h
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	pngenc "image/png"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"golang.org/x/sys/unix"
)

// fits is the message screen's text metrics for a panel of this width: how big to draw, how far in
// to start, and how many characters then fit on a line.
//
// basicfont advances 7 pixels a character, so a line is 7*scale wide per character and the margins
// take the rest. The numbers are checked against a counted ruler string rather than trusted: a line
// that is one character too long does not wrap, it runs off the panel, and the line it eats is the
// one telling somebody what to do.
//
// A Show 5 is 960 across and takes scale 3; a Spot is 480 and would fit 19 characters at that size,
// which is not a sentence, so it drops to scale 2 and a narrower margin. A Show 8 is 1280 and has
// room to spare.
func fits(w int) (scale, margin, chars int) {
	scale, margin = 3, 40
	if w < 720 {
		scale, margin = 2, 20
	}
	return scale, margin, (w - 2*margin) / (7 * scale)
}

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

// background is the color behind everything: the mark's own dark brown, or whatever -fill asked for.
func background(fill string) color.RGBA {
	bg := color.RGBA{0x1c, 0x15, 0x11, 0xff}
	if fill != "" {
		if c, err := strconv.ParseUint(fill, 16, 32); err == nil {
			bg = color.RGBA{uint8(c >> 16), uint8(c >> 8), uint8(c), 0xff}
		}
	}
	return bg
}

// compose draws the landscape image, and says where the clock goes so the repaint can clear only
// that much. Kept apart from the framebuffer so -png can render exactly what the panel would show
// on a machine with no panel: wording that nobody can see before it is on a device is wording that
// gets shipped wrong.
func compose(w, h int, bg color.RGBA, fill, title, lines string) (*image.RGBA, image.Rectangle, int, int) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)

	amber := color.RGBA{0xe9, 0xa2, 0x3b, 0xff}
	paper := color.RGBA{0xe8, 0xdc, 0xc8, 0xff}
	clockAt := image.Rect(40, 250, w-40, 360)
	clockY, clockScale := 300, 6

	switch {
	case fill != "":
		// Nothing but the color: the caller wants the panel proved, not described.

	case title != "":
		// Something to say, which on this device means the rescue environment saying so. The test
		// image is not drawn: color bars beside an explanation read as a fault in the explanation.
		scale, margin, chars := fits(w)
		step := 13*scale + 5             // a line, plus enough that the rows do not touch
		top := 40 + 13*(scale+3) + scale // under the heading, which is drawn three sizes larger

		frame(img, img.Bounds().Inset(8), 4, amber)
		text(img, title, margin, 40, scale+3, amber)
		for i, l := range strings.Split(lines, "|") {
			if l = strings.TrimSpace(l); l != "" {
				if len(l) > chars {
					l = l[:chars]
				}
				text(img, l, margin, top+i*step, scale, paper)
			}
		}
		// Bottom left, clear of the lines and clear of the bottom edge, and still ticking: a clock
		// that moves is how somebody in front of the device tells this screen from a frozen one.
		clockAt = image.Rect(margin, h-90, margin+260, h-18)
		clockY, clockScale = h-70, scale+1
		text(img, time.Now().Format("15:04:05"), margin, clockY, clockScale, paper)

	default:
		// Color bars along the bottom, an amber frame, and text.
		bars := []color.RGBA{{0xff, 0, 0, 0xff}, {0, 0xff, 0, 0xff}, {0, 0, 0xff, 0xff}, {0xff, 0xff, 0xff, 0xff}, {0xe9, 0xa2, 0x3b, 0xff}}
		bw := w / len(bars)
		for i, c := range bars {
			draw.Draw(img, image.Rect(i*bw, h-60, (i+1)*bw, h), image.NewUniform(c), image.Point{}, draw.Src)
		}
		frame(img, img.Bounds().Inset(8), 4, amber)
		text(img, "TECHO5", 40, 120, 8, amber)
		text(img, fmt.Sprintf("framebuffer %dx%d", w, h), 40, 220, 3, paper)
		text(img, time.Now().Format("15:04:05"), 40, 300, 6, paper)
	}
	return img, clockAt, clockY, clockScale
}

func main() {
	info := flag.Bool("info", false, "print framebuffer geometry and exit")
	png := flag.String("png", "", "write what the panel would show to this file and exit (no device needed)")
	pw := flag.Int("png-w", 960, "canvas width for -png (960 Show 5, 480 Spot, 1280 Show 8)")
	ph := flag.Int("png-h", 480, "canvas height for -png")
	fill := flag.String("fill", "", "fill with this rrggbb color only")
	hold := flag.Duration("hold", 0, "keep repainting for this long (0 = paint once and exit)")
	title := flag.String("title", "", "a heading to draw instead of the test image")
	lines := flag.String("lines", "", "lines under the heading, separated by | (needs -title)")
	mapSize := flag.Int("map", 0, "try this mmap size first, in bytes")
	flag.Parse()

	// Before the device is opened, so this works on a workstation: it is how the rescue wording is
	// read by somebody who is not standing in front of a unit.
	if *png != "" {
		img, _, _, _ := compose(*pw, *ph, background(*fill), *fill, *title, *lines)
		out, err := os.Create(*png)
		if err != nil {
			fatal("create %s: %v", *png, err)
		}
		defer out.Close()
		if err := pngenc.Encode(out, img); err != nil {
			fatal("encode %s: %v", *png, err)
		}
		fmt.Printf("wrote %s (%dx%d)\n", *png, *pw, *ph)
		return
	}

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
	bg := background(*fill)

	img, clockAt, clockY, clockScale := compose(w, h, bg, *fill, *title, *lines)

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
				draw.Draw(img, clockAt, image.NewUniform(bg), image.Point{}, draw.Src)
				text(img, time.Now().Format("15:04:05"), 40, clockY, clockScale, color.RGBA{0xe8, 0xdc, 0xc8, 0xff})
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
			pixel |= uint32(a) << v.Transp[0]
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
