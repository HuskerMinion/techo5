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
//	fbprobe -dump shot.png  # write what the panel is showing now to a PNG
//	fbprobe -title RESCUE -lines "first line|second line" -hold 1000h
//	fbprobe -watch -watch-for 30s   # how often the panel's page changes, reading only
//	fbprobe -bench 300             # pan as fast as the driver takes it, timing fill and pan
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	pngenc "image/png"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"golang.org/x/sys/unix"

	xdraw "golang.org/x/image/draw"
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
	BitsPerPixel, Grayscale                                uint32
	Red, Green, Blue, Transp                               [3]uint32 // offset, length, msb_right
	Nonstd, Activate, Height, Width, AccelFlags            uint32
	Pixclock, LeftMargin, RightMargin, UpperMargin         uint32
	LowerMargin, HsyncLen, VsyncLen, Sync, Vmode, Rotate   uint32
	Colorspace                                             uint32
	Reserved                                               [4]uint32
}

// fb_fix_screeninfo on a 32-bit kernel ABI... this kernel is arm64 with a 32-bit userspace, so the
// compat layout applies: unsigned long is 4 bytes here.
type fixInfo struct {
	ID                    [16]byte
	SmemStart             uint32
	SmemLen               uint32
	Type, TypeAux, Visual uint32
	Xpanstep, Ypanstep    uint16
	Ywrapstep             uint16
	_                     uint16
	LineLength            uint32
	MmioStart             uint32
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
	dump := flag.String("dump", "", "write the page the panel is showing to this PNG and exit")
	decodeDir := flag.String("decode", "", "time decoding every .jpg in this directory and putting it on the panel")
	decodeN := flag.Int("decode-n", 10, "how many times -decode times each frame")
	watch := flag.Bool("watch", false, "report how often the panel's page changes (reads only)")
	watchFor := flag.Duration("watch-for", 30*time.Second, "how long -watch runs (0 = until interrupted)")
	watchEach := flag.Bool("watch-each", false, "print every frame -watch sees")
	bench := flag.Int("bench", 0, "pan the panel this many times, as fast as the driver takes it")
	benchFor := flag.Duration("bench-for", 0, "stop -bench after this long (0 = by count alone)")
	benchInterval := flag.Duration("bench-interval", 0, "pace -bench to this interval (0 = as fast as possible)")
	benchEach := flag.Bool("bench-each", false, "print every -bench frame")
	fill2 := flag.String("fill2", "ffffff", "the other color -bench alternates with")
	flag.Parse()

	// -watch reads and changes nothing, so it runs while the daemon is still working: what the
	// daemon presents while it is working is the question, and a frozen daemon answers another one.
	if *watch {
		wf, err := os.Open(fbDev)
		if err != nil {
			fatal("open %s: %v", fbDev, err)
		}
		defer wf.Close()
		if err := watchFrames(wf, *watchFor, *watchEach); err != nil {
			fatal("watch: %v", err)
		}
		return
	}

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

	// -dump reads the page the panel is on and writes it out, so what is on the glass can be looked
	// at from somewhere else. It reads only.
	if *dump != "" {
		if err := dumpPanel(mem, v, int(fx.LineLength), int(v.Xres), int(v.Yres), *dump); err != nil {
			fatal("dump: %v", err)
		}
		return
	}

	// -decode times the path a streamed frame takes, and writes to the panel to do it.
	if *decodeDir != "" {
		kept := keepPanel(mem, int(fx.LineLength), int(v.Yres), pageCount(v), v)
		guardPanel(kept, f, mem, v)
		err := decodeCost(f, mem, v, int(fx.LineLength), int(v.Xres), int(v.Yres), *decodeDir, *decodeN)
		kept.restore(f, mem, v)
		if err != nil {
			fatal("decode: %v", err)
		}
		return
	}

	// -bench drives the panel itself. The daemon must be frozen first: it presents to the same
	// panel, and two writers to one framebuffer measure each other rather than the driver.
	if *bench > 0 || *benchFor > 0 {
		pid := daemonPID()
		if pid != "" {
			fmt.Printf("note: the daemon (pid %s) is running; freeze it with 'kill -STOP %s' or these\n"+
				"numbers are the two of you sharing the panel, not what one of you can do.\n", pid, pid)
		}
		if err := benchPresent(f, mem, v, int(fx.LineLength), int(v.Xres), int(v.Yres),
			pageCount(v), *bench, *benchInterval, *benchFor, background(*fill), background(*fill2),
			*benchEach); err != nil {
			fatal("bench: %v", err)
		}
		return
	}

	// The panel is portrait (480 wide, 960 tall); the device is used landscape. Compose a 960x480
	// landscape image and rotate it 90 degrees clockwise onto the panel.
	w, h := int(v.Yres), int(v.Xres) // landscape canvas
	bg := background(*fill)

	img, clockAt, clockY, clockScale := compose(w, h, bg, *fill, *title, *lines)

	// What this paints must be put back when it finishes. The daemon skips rows it believes it has
	// already drawn (screen.go's shadow), so a page left as another program painted it stays that way —
	// static text and all — until something on it moves, which for static text is never. This is also
	// what the rescue environment does not need: there is no daemon running to disagree with.
	kept := keepPanel(mem, int(fx.LineLength), int(v.Yres), pageCount(v), v)
	defer kept.restore(f, mem, v)

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

// guardPanel puts the panel back if the process is interrupted. An interrupted run would otherwise
// leave the daemon's shadow disagreeing with the screen for good, which is the one outcome none of
// these modes may have. Best effort: on a signal anything already half written is left as it is, and
// the restore that matters is the one the mode does on its way out.
func guardPanel(kept *panelState, f *os.File, mem []byte, v varInfo) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ch
		kept.restore(f, mem, v)
		os.Exit(1)
	}()
}

// decodeCost times what one streamed frame costs the device, in the three parts it arrives in:
// decoding the JPEG, scaling it to the panel, and putting the frame up. They are reported apart
// because they scale with different things — the decode with the source's pixels, the write with the
// panel's — so a stream can trade one against the other by sending smaller pictures.
//
// Every file is scaled to fill the whole panel, which is what a video page has to do, so the write is
// the same for all of them and what differs is the decode and the scale. Decoding is timed per file,
// on that file, which is the whole point of separating them.
//
// Note which write this is: fbprobe's own blit writes every pixel and skips nothing, where the daemon's
// Present compares each row against its shadow first. So there is no skipping to time here, and a frame
// is timed whether or not the one before it was the same picture.
//
// The write is fbprobe's own rotate, which is the same class of work as the daemon's: a per-pixel
// pass over the canvas packing each pixel for the panel. It is the closest this can come to the
// daemon's Present without being the daemon.
func decodeCost(f *os.File, mem []byte, v varInfo, line, panelW, panelH int, dir string, iters int) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var files []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		if n := strings.ToLower(e.Name()); strings.HasSuffix(n, ".jpg") || strings.HasSuffix(n, ".jpeg") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	if len(files) == 0 {
		return fmt.Errorf("no .jpg files in %s", dir)
	}
	sort.Strings(files)
	if iters < 1 {
		iters = 1
	}

	w, h := panelH, panelW // landscape, the way it is composed
	canvas := image.NewRGBA(image.Rect(0, 0, w, h))
	fmt.Printf("decode: %dx%d panel, %d frame(s) from %s, %d pass(es), frames used in turn\n\n",
		w, h, len(files), dir, iters)

	var best float64
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}

		var decodes, scales, writes, totals []time.Duration
		for i := 0; i < iters; i++ {
			t0 := time.Now()
			img, err := jpeg.Decode(bytes.NewReader(raw))
			t1 := time.Now()
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			xdraw.NearestNeighbor.Scale(canvas, canvas.Bounds(), img, img.Bounds(), draw.Src, nil)
			t2 := time.Now()
			blit(mem, canvas, line, panelW, panelH, v)
			v.Xoffset, v.Yoffset = 0, 0
			if err := ioctl(f.Fd(), fbioPanDisplay, unsafe.Pointer(&v)); err != nil {
				return fmt.Errorf("FBIOPAN_DISPLAY: %w", err)
			}
			t3 := time.Now()
			decodes = append(decodes, t1.Sub(t0))
			scales = append(scales, t2.Sub(t1))
			writes = append(writes, t3.Sub(t2))
			totals = append(totals, t3.Sub(t0))
		}

		d, _, _, dmax := spread(append([]time.Duration(nil), decodes...))
		_, s50, _, smax := spread(append([]time.Duration(nil), scales...))
		_, w50, _, wmax := spread(append([]time.Duration(nil), writes...))
		_, t50, _, _ := spread(append([]time.Duration(nil), totals...))
		fps := 1 / t50.Seconds()
		if fps > best {
			best = fps
		}
		fmt.Printf("%-16s %4dx%-4d %7d B\n", filepath.Base(path), cfg.Width, cfg.Height, len(raw))
		fmt.Printf("    decode %v (max %v)   scale %v (max %v)\n",
			d.Round(time.Millisecond), dmax.Round(time.Millisecond), s50.Round(time.Millisecond), smax.Round(time.Millisecond))
		fmt.Printf("    write+pan %v (max %v)   total %v  ->  %.1f frames/s  (%.2f MB/s of pictures)\n\n",
			w50.Round(time.Millisecond), wmax.Round(time.Millisecond), t50.Round(time.Millisecond), fps, float64(len(raw))*fps/1e6)
	}
	if best > 0 {
		fmt.Printf("best case above: %.1f frames/s\n", best)
	}
	return nil
}

// panelState is the pages as they were found and the page that was on show, so that whatever a mode
// paints can be put back.
//
// The daemon skips rows it believes it has already drawn: it keeps a shadow of every page and compares
// against it, so anything written to the framebuffer behind its back is treated as already on screen
// and never corrected. Static content — a weather line, a date, the background under fixed text — is
// then gone for good, because a row that never changes never differs from the shadow again. Nothing
// short of a daemon restart puts it right, so every mode that writes has to leave what it found.
//
// The rescue environment is the one place this does not matter: there is no daemon to disagree with.
type panelState struct {
	was    uint32
	backup []byte
}

// keepPanel copies the whole framebuffer out and remembers which page was on show.
func keepPanel(mem []byte, line, panelH, pages int, v varInfo) *panelState {
	n := line * panelH * pages
	if n <= 0 || n > len(mem) {
		n = len(mem)
	}
	s := &panelState{was: v.Yoffset, backup: make([]byte, n)}
	copy(s.backup, mem[:n])
	return s
}

// restore puts the pages back and pans to the page that was on show.
func (s *panelState) restore(f *os.File, mem []byte, v varInfo) {
	copy(mem[:len(s.backup)], s.backup)
	v.Xoffset, v.Yoffset = 0, s.was
	if err := ioctl(f.Fd(), fbioPanDisplay, unsafe.Pointer(&v)); err != nil {
		fmt.Fprintf(os.Stderr, "fbprobe: putting the panel back: %v\n", err)
		return
	}
	fmt.Printf("panel put back: %d bytes restored and panned to yoffset %d\n", len(s.backup), s.was)
}

// dumpPanel writes the page the panel is showing, turned back the way it is composed so that it
// reads as the thing somebody designed rather than as the panel's portrait rows. The pan offset is
// the page on show, which is what makes this a picture of the glass and not of a buffer: page zero
// is very often not what is up.
func dumpPanel(mem []byte, v varInfo, line, panelW, panelH int, path string) error {
	pages := pageCount(v)
	page := 0
	if panelH > 0 {
		page = int(v.Yoffset) / panelH
	}
	if page < 0 || page >= pages {
		page = 0
	}
	base := page * panelH * line
	if base < 0 || base+panelH*line > len(mem) {
		return fmt.Errorf("page %d of %d is outside the mapping", page, pages)
	}

	// Landscape, the way it was drawn: landscape (x, y) is panel (panelW-1-y, x).
	w, h := panelH, panelW
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			off := base + x*line + (panelW-1-y)*4
			word := binary.LittleEndian.Uint32(mem[off : off+4])
			i := img.PixOffset(x, y)
			img.Pix[i+0] = byte(word >> v.Red[0])
			img.Pix[i+1] = byte(word >> v.Green[0])
			img.Pix[i+2] = byte(word >> v.Blue[0])
			img.Pix[i+3] = byte(word >> v.Transp[0])
		}
	}

	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	if err := pngenc.Encode(out, img); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%dx%d, page %d of %d at yoffset %d)\n", path, w, h, page, pages, v.Yoffset)
	return nil
}

// pageCount is how many pages the framebuffer holds. The driver keeps several so that a frame is
// never seen half written, which is also why the pan offset is a page and not a guess.
func pageCount(v varInfo) int {
	if v.Yres > 0 && v.YresVirtual > v.Yres {
		return int(v.YresVirtual / v.Yres)
	}
	return 1
}

// pageOffsets names where a device's pages sit, for a line somebody has to read.
func pageOffsets(pages, yres int) string {
	if pages < 2 {
		return "not at all (one page)"
	}
	out := make([]string, 0, pages)
	for i := 0; i < pages; i++ {
		out = append(out, strconv.Itoa(i*yres))
	}
	return strings.Join(out, ", ")
}

// daemonPID is the daemon's pid if it is running, found the way the rescue notes do: by the name the
// kernel knows it by. Best effort, since it only decorates a warning.
func daemonPID() string {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return ""
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/comm")
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(b)) == "techo5" {
			return e.Name()
		}
	}
	return ""
}

// spread sorts ds and returns its smallest, middle, 95th and largest.
func spread(ds []time.Duration) (min, p50, p95, max time.Duration) {
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	at := func(q float64) time.Duration { return ds[int(q*float64(len(ds)-1))] }
	return ds[0], at(0.5), at(0.95), ds[len(ds)-1]
}

// counts is a page tally in offset order, for a line somebody has to read: "0 x15, 960 x14".
func counts(m map[uint32]int) string {
	keys := make([]uint32, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%d x%d", k, m[k]))
	}
	return strings.Join(out, ", ")
}

// watchFrames reports how often the panel's page actually changes. It only reads: nothing is written
// to the framebuffer and nothing is asked of the driver that changes it, so it can be run against a
// device whose daemon is still working. That is the point of it: what the daemon presents while it
// is working is the question, and a frozen daemon answers a different one.
//
// Each change of the pan offset is one frame the driver was told to show, and the pan does not
// return until the command queue has run it (mtkfb_pan_display_impl asks for a blocking flush), so
// this counts frames put up rather than frames asked for. What it cannot see is the glass: the right
// page at the wrong moment looks perfect here, which is why a panel's own faults still need a camera.
//
// The sampling interval is the sleep below. Five thousand looks a second sample a 33 ms frame 150
// times and time it to about a fifth of a millisecond, which is finer than anything being decided
// here, and it costs a fraction of one of the device's four cores rather than all of it.
func watchFrames(f *os.File, forDur time.Duration, each bool) error {
	const poll = 200 * time.Microsecond

	var v varInfo
	if err := ioctl(f.Fd(), fbioGetVScreenInfo, unsafe.Pointer(&v)); err != nil {
		return fmt.Errorf("FBIOGET_VSCREENINFO: %w", err)
	}
	pages := pageCount(v)
	fmt.Printf("fb %dx%d, %d page(s) of %d rows: yoffset cycles %s\n",
		v.Xres, v.Yres, pages, v.Yres, pageOffsets(pages, int(v.Yres)))
	runFor := "until interrupted"
	if forDur > 0 {
		runFor = "for " + forDur.String()
	}
	fmt.Printf("watching %s, reading only (the daemon is left running)\n\n", runFor)

	cur := v.Yoffset
	start, prev := time.Now(), time.Now()
	var gaps []time.Duration
	seen := map[uint32]int{}
	for {
		v = varInfo{}
		if err := ioctl(f.Fd(), fbioGetVScreenInfo, unsafe.Pointer(&v)); err != nil {
			return fmt.Errorf("FBIOGET_VSCREENINFO: %w", err)
		}
		now := time.Now()
		if v.Yoffset != cur {
			cur = v.Yoffset
			gaps = append(gaps, now.Sub(prev))
			prev = now
			seen[cur]++
			if each {
				fmt.Printf("  %8.3fs  frame %5d  yoffset %6d  interval %v\n",
					now.Sub(start).Seconds(), len(gaps), cur, gaps[len(gaps)-1].Round(time.Microsecond))
			}
		}
		if forDur > 0 && now.Sub(start) >= forDur {
			break
		}
		time.Sleep(poll)
	}

	elapsed := time.Since(start)
	fmt.Printf("\n%d frames in %v: %.1f frames/s\n",
		len(gaps), elapsed.Round(time.Millisecond), float64(len(gaps))/elapsed.Seconds())
	if len(gaps) > 0 {
		mn, p50, p95, mx := spread(gaps)
		fmt.Printf("interval min %v, p50 %v, p95 %v, max %v\n",
			mn.Round(time.Microsecond), p50.Round(time.Microsecond), p95.Round(time.Microsecond), mx.Round(time.Microsecond))
	}
	if len(seen) > 0 {
		fmt.Printf("pages seen: %s\n", counts(seen))
	}
	return nil
}

// fillPage writes one color over a whole framebuffer page, doubling a word rather than looping a
// pixel at a time: the driver is what is being measured, so the write is kept out of the way and
// what it costs to render a real page is added on top of the figure this gives.
func fillPage(page []byte, c color.RGBA, v varInfo) {
	var px uint32
	px |= uint32(c.R) << v.Red[0]
	px |= uint32(c.G) << v.Green[0]
	px |= uint32(c.B) << v.Blue[0]
	px |= uint32(c.A) << v.Transp[0]
	var word [4]byte
	binary.LittleEndian.PutUint32(word[:], px)

	// A page is a whole row of a panel, so this is only to make the function total: what is shorter
	// than a pixel has no whole word to double, and the doubling below would not keep the pattern's
	// four-byte period in any case.
	if len(page) < len(word) {
		copy(page, word[:len(page)])
		return
	}
	n := copy(page, word[:])
	for n < len(page) {
		n += copy(page[n:], page[:n])
	}
}

// verdict says what one pan costs, since that is the number the whole bench exists for. The panel
// scans out at 59.64 Hz, so a pan of about 16.7 ms is one frame period and the driver pacing itself
// against the panel; about twice that is two, and the driver is the ceiling near 30 frames a second.
// It is deliberately a reading and not a law: what the pan waits for is the command queue, and a
// device under load can take longer for reasons that have nothing to do with the panel.
func verdict(pan, pace time.Duration, late int) string {
	switch {
	case pace > 0 && late > 0:
		return fmt.Sprintf("asked for %v and did not keep it: %d frame(s) went late", pace, late)
	case pan >= 30*time.Millisecond:
		return "a pan costs about two frame periods: expect a ceiling near 30 frames/s"
	case pan >= 12*time.Millisecond:
		return "a pan costs about one frame period (59.64 Hz panel): the driver paces itself at vsync"
	default:
		return "a pan returns in well under a frame period: the panel is not the ceiling here"
	}
}

// paceNote is how a bench run was paced, in words, with the rate that comes of it.
func paceNote(pace time.Duration) string {
	if pace <= 0 {
		return ", as fast as the driver takes it"
	}
	return fmt.Sprintf(", paced to %v (%.1f frames/s)", pace, 1/pace.Seconds())
}

// benchPresent drives the panel as fast as the driver will take it, and times the two halves of a
// frame apart: writing the page, and the pan that puts it up. The pan is the half that sets the
// ceiling, because it is the driver's own cost and is the same for every program that presents; the
// write here is a bare fill, so a daemon that renders into its page adds its own work on top and
// presents more slowly than this. For what the daemon actually manages, use -watch.
//
// Every frame a different color, over the whole panel, deliberately: the screen's Present skips rows
// that have not changed, so a bench that redraws one picture measures the skipping and not the
// presenting, and reports a rate no moving content could reach. The pages are cycled the way the
// daemon cycles them, so the pattern this paints is also one a camera can count.
func benchPresent(f *os.File, mem []byte, v varInfo, line, panelW, panelH, pages, iters int,
	pace, forDur time.Duration, a, b color.RGBA, each bool) error {
	pageBytes := line * panelH
	if pages < 1 || pageBytes <= 0 {
		return fmt.Errorf("no frame fits the mapping (%d by %d, %d pages)", line, panelH, pages)
	}
	if need := pageBytes * pages; len(mem) < need {
		return fmt.Errorf("mapped %d bytes, need %d for %d pages", len(mem), need, pages)
	}

	// Nothing here may leave the panel holding something the daemon does not know about; see keepPanel.
	kept := keepPanel(mem, line, panelH, pages, v)
	defer kept.restore(f, mem, v)
	fmt.Printf("bench: %dx%d, %d page(s), colors %02x%02x%02x / %02x%02x%02x%s\n",
		panelW, panelH, pages, a.R, a.G, a.B, b.R, b.G, b.B, paceNote(pace))

	// The first pan after the geometry is set is the one the driver may ignore (no_update in mtkfb.c),
	// and it is the one that puts the offset where the loop below assumes it already is. Done here so
	// that it is not counted as an unusually fast frame.
	fillPage(mem[:pageBytes], a, v)
	v.Xoffset, v.Yoffset = 0, 0
	if err := ioctl(f.Fd(), fbioPanDisplay, unsafe.Pointer(&v)); err != nil {
		return fmt.Errorf("FBIOPAN_DISPLAY (warm-up): %w", err)
	}

	start := time.Now()
	var fills, pans []time.Duration
	late := 0
	for n := 0; ; n++ {
		if iters > 0 && n >= iters {
			break
		}
		if forDur > 0 && time.Since(start) >= forDur {
			break
		}
		page := n % pages
		c := a
		if n%2 == 1 {
			c = b
		}

		t0 := time.Now()
		fillPage(mem[page*pageBytes:(page+1)*pageBytes], c, v)
		t1 := time.Now()
		v.Xoffset, v.Yoffset = 0, uint32(page*panelH)
		err := ioctl(f.Fd(), fbioPanDisplay, unsafe.Pointer(&v))
		t2 := time.Now()
		if err != nil {
			return fmt.Errorf("FBIOPAN_DISPLAY: %w", err)
		}
		fills = append(fills, t1.Sub(t0))
		pans = append(pans, t2.Sub(t1))
		if each {
			fmt.Printf("  frame %5d  page %d  fill %v  pan %v\n", n, page,
				t1.Sub(t0).Round(time.Microsecond), t2.Sub(t1).Round(time.Microsecond))
		}
		if pace > 0 {
			if wait := start.Add(time.Duration(n+1) * pace).Sub(time.Now()); wait > 0 {
				time.Sleep(wait)
			} else {
				late++
			}
		}
	}

	elapsed := time.Since(start)
	n := len(pans)
	fmt.Printf("\n%d frames in %v: %.1f frames/s\n",
		n, elapsed.Round(time.Millisecond), float64(n)/elapsed.Seconds())
	if n > 0 {
		_, f50, _, fmax := spread(fills)
		mn, p50, p95, mx := spread(pans)
		fmt.Printf("fill p50 %v, max %v\n", f50.Round(time.Microsecond), fmax.Round(time.Microsecond))
		fmt.Printf("pan  min %v, p50 %v, p95 %v, max %v\n",
			mn.Round(time.Microsecond), p50.Round(time.Microsecond), p95.Round(time.Microsecond), mx.Round(time.Microsecond))
		fmt.Printf("%s\n", verdict(p50, pace, late))
	}
	return nil
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "fbprobe: "+format+"\n", a...)
	os.Exit(1)
}
