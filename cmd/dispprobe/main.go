//go:build linux

// dispprobe paints a test picture on the Echo Show 5's panel through the MediaTek display
// controller (see internal/mtkdisp). It is the first proof that TECHO5 can own the screen
// without Android's SurfaceFlinger and hardware composer.
//
// Stop the composer first, or the two will fight over the overlay layers:
//
//	stop vendor.hwcomposer-2-1
//	dispprobe -hold 20s
//	dispprobe -fill 1c1511
//	dispprobe -info
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"strconv"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"github.com/HuskerMinion/techo5/internal/mtkdisp"
)

func main() {
	info := flag.Bool("info", false, "print display information and exit")
	fill := flag.String("fill", "", "fill with this rrggbb color only")
	hold := flag.Duration("hold", 0, "keep repainting (with a clock) for this long; 0 paints once and exits")
	interval := flag.Duration("interval", time.Second, "repaint interval while holding")
	layer := flag.Int("layer", 0, "overlay layer to use")
	formatName := flag.String("fmt", "bgra", "pixel format the controller is told: bgra or rgba")
	scan := flag.Int("scan", 0, "debug: find the struct size the kernel accepts for this DISP ioctl number and exit")
	capture := flag.String("capture", "", "read back what the panel shows into this PNG (landscape) and exit")
	vsync := flag.Bool("vsync", false, "wait for three vertical syncs, print their timing, and exit")
	mode := flag.Int("mode", 0, "set the session mode first: 1 direct link, 2 decouple (0 leaves it)")
	regs := flag.Bool("regs", false, "dump the overlay and RDMA registers (also after painting, when combined)")
	flag.Parse()

	dumpRegs := func(when string) {
		r, err := mtkdisp.ReadRegs()
		if err != nil {
			fmt.Fprintf(os.Stderr, "regs: %v\n", err)
			return
		}
		fmt.Printf("registers %s:\n  OVL0 sta %08x en %08x roi %08x bg %08x src_con %08x\n",
			when, r.OvlSta, r.OvlEn, r.OvlRoiSize, r.OvlBgColor, r.OvlSrcCon)
		for i, l := range r.Layer {
			fmt.Printf("  L%d %s con %08x size %08x off %08x pitch %08x addr %08x\n",
				i, map[bool]string{true: "on ", false: "off"}[r.Enabled(i)], l.Con, l.SrcSize, l.Offset, l.Pitch, l.Addr)
		}
		fmt.Printf("  RDMA0 global %08x size0 %08x size1 %08x memcon %08x memstart %08x pitch %08x\n",
			r.RdmaGlobalCon, r.RdmaSizeCon0, r.RdmaSizeCon1, r.RdmaMemCon, r.RdmaMemStart, r.RdmaMemPitch)
	}
	if *regs {
		dumpRegs("now")
	}

	if *scan != 0 {
		for _, hit := range mtkdisp.ScanIoctlSize(*scan, 8, 4096) {
			fmt.Printf("ioctl nr %d accepted with %d-byte argument\n", *scan, hit)
		}
		return
	}
	if awake, err := mtkdisp.Awake(); err != nil {
		fmt.Fprintf(os.Stderr, "power state: %v\n", err)
	} else {
		fmt.Printf("panel path awake: %v\n", awake)
	}

	format := mtkdisp.FormatBGRA8888
	if *formatName == "rgba" {
		format = mtkdisp.FormatRGBA8888
	}

	d, err := mtkdisp.Open()
	if err != nil {
		fatal("%v", err)
	}
	defer d.Close()
	fmt.Printf("display: session 0x%x, %dx%d @ %d Hz, format 0x%x, %d layers, physical %dx%d mm\n",
		d.Session(), d.Info.Width, d.Info.Height, d.Info.VsyncFPS, d.Info.Format, d.Info.MaxLayers,
		d.Info.PhysicalMM[0], d.Info.PhysicalMM[1])
	if *info {
		return
	}
	if *mode != 0 {
		if err := d.SetMode(uint32(*mode)); err != nil {
			fatal("%v", err)
		}
		fmt.Printf("session mode set to %d\n", *mode)
	}
	if *vsync {
		for i := 0; i < 3; i++ {
			n, ts, err := d.WaitVsync()
			if err != nil {
				fatal("vsync: %v", err)
			}
			fmt.Printf("vsync %d at %v\n", n, ts)
		}
		return
	}
	if *capture != "" {
		raw, err := mtkdisp.Capture(d.Info.Width, d.Info.Height)
		if err != nil {
			fatal("%v", err)
		}
		// Rotate the portrait capture back to landscape for viewing: panel (px, py) came from
		// landscape (py, panelW-1-px).
		pw, ph := d.Info.Width, d.Info.Height
		shot := image.NewRGBA(image.Rect(0, 0, ph, pw))
		for py := 0; py < ph; py++ {
			for px := 0; px < pw; px++ {
				o := (py*pw + px) * 4
				x, y := py, pw-1-px
				i := shot.PixOffset(x, y)
				shot.Pix[i+0] = raw[o+2]
				shot.Pix[i+1] = raw[o+1]
				shot.Pix[i+2] = raw[o+0]
				shot.Pix[i+3] = 0xff
			}
		}
		lit := 0
		for i := 0; i < len(raw); i += 4 {
			if raw[i] != 0 || raw[i+1] != 0 || raw[i+2] != 0 {
				lit++
			}
		}
		f, err := os.Create(*capture)
		if err != nil {
			fatal("%v", err)
		}
		defer f.Close()
		if err := png.Encode(f, shot); err != nil {
			fatal("png: %v", err)
		}
		fmt.Printf("captured %s: %d of %d pixels not black\n", *capture, lit, len(raw)/4)
		return
	}

	pw, ph := d.Info.Width, d.Info.Height // panel, portrait
	bufs := make([]*mtkdisp.Buffer, 2)
	for i := range bufs {
		bufs[i], err = d.NewBuffer(pw, ph)
		if err != nil {
			fatal("%v", err)
		}
		defer bufs[i].Free()
	}
	fmt.Printf("buffers: %d x %d bytes\n", len(bufs), len(bufs[0].Mem))
	for i, b := range bufs {
		mva, err := b.MVA()
		if err != nil {
			fmt.Fprintf(os.Stderr, "buffer %d mva: %v\n", i, err)
			continue
		}
		fmt.Printf("buffer %d device address (mva): 0x%08x\n", i, mva)
	}

	// Compose landscape (the way the device sits), rotate onto the portrait panel.
	w, h := ph, pw
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	bg := color.RGBA{0x1c, 0x15, 0x11, 0xff}
	if *fill != "" {
		if c, err := strconv.ParseUint(*fill, 16, 32); err == nil {
			bg = color.RGBA{uint8(c >> 16), uint8(c >> 8), uint8(c), 0xff}
		}
	}
	amber := color.RGBA{0xe9, 0xa2, 0x3b, 0xff}
	ink := color.RGBA{0xe8, 0xdc, 0xc8, 0xff}
	draw.Draw(img, img.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
	if *fill == "" {
		bars := []color.RGBA{{0xff, 0, 0, 0xff}, {0, 0xff, 0, 0xff}, {0, 0, 0xff, 0xff}, {0xff, 0xff, 0xff, 0xff}, amber}
		bw := w / len(bars)
		for i, c := range bars {
			draw.Draw(img, image.Rect(i*bw, h-60, (i+1)*bw, h), image.NewUniform(c), image.Point{}, draw.Src)
		}
		frame(img, img.Bounds().Inset(8), 4, amber)
		text(img, "TECHO5", 40, 100, 8, amber)
		text(img, "red green blue white amber", 40, 210, 3, ink)
		text(img, time.Now().Format("15:04:05"), 40, 290, 6, ink)
	}

	frameN, fenceTimeouts := 0, 0
	post := func() {
		b := bufs[frameN%len(bufs)]
		frameN++
		if err := b.Wait(100 * time.Millisecond); err != nil {
			fenceTimeouts++
		}
		blit(b, img)
		if err := d.Post(*layer, b, format); err != nil {
			fatal("post: %v", err)
		}
	}
	post()
	fmt.Println("posted")
	if *regs {
		time.Sleep(100 * time.Millisecond)
		dumpRegs("after post")
	}
	if *hold > 0 {
		deadline := time.Now().Add(*hold)
		for time.Now().Before(deadline) {
			time.Sleep(*interval)
			if *fill == "" {
				draw.Draw(img, image.Rect(40, 250, w-40, 380), image.NewUniform(bg), image.Point{}, draw.Src)
				text(img, time.Now().Format("15:04:05.000"), 40, 290, 6, ink)
			}
			post()
		}
		fmt.Printf("posted %d frames, %d release fences never signaled\n", frameN, fenceTimeouts)
	}
}

// blit writes the landscape image onto the portrait buffer rotated 90 degrees clockwise:
// landscape (x, y) lands at panel (panelW-1-y, x). Bytes are B, G, R, A (BGRA8888).
func blit(b *mtkdisp.Buffer, img *image.RGBA) {
	line := b.Pitch * 4
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			i := img.PixOffset(x, y)
			px, py := b.Width-1-y, x
			off := py*line + px*4
			b.Mem[off+0] = img.Pix[i+2] // B
			b.Mem[off+1] = img.Pix[i+1] // G
			b.Mem[off+2] = img.Pix[i+0] // R
			b.Mem[off+3] = img.Pix[i+3] // A
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

// text draws with the built-in 7x13 face scaled by an integer factor: crude, and readable from
// across a desk. A real face comes with the display layer proper.
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
	fmt.Fprintf(os.Stderr, "dispprobe: "+format+"\n", a...)
	os.Exit(1)
}
