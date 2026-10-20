// isp2cmp: the host side of the pass-2 experiments. It makes test frames in the camera's packed
// RAW10, develops them on the CPU the way echod does, turns the ISP's YUV422 output into pictures,
// and compares the two.
//
//	isp2cmp mkraw  -o in.raw [-photo p.png] [-black 16] [-wbr 0.55] [-wbb 0.7] [-truth scene.lin] [-noise 1.5 -shot 0.2]
//	isp2cmp ref    -raw in.raw -o ref.yuv [-png ref.png] [-black 0] [-white 2046] [-gr 1] [-gb 1] [-gamma 1]
//	               [-demosaic bilinear|cell] [-range full|limited]
//	isp2cmp show   -yuv out.yuv -o out.png [-w 1600] [-h 1200] [-stride 3200] [-order yuyv] [-signed]
//	isp2cmp tdr    -o tdr.bin [-w 1600] [-h 1200] [-pixid 3] [-last-only=true] [-check lib.bin]
//	isp2cmp tone|ctl|i420|conv|cmp  (stage 3, develop.go)
//	isp2cmp psnr   -a ref.yuv -b isp.yuv [-w 1600] [-h 1200] [-stride 3200] [-order yuyv] [-signed]
//	               [-x0 0] [-x1 1600] [-diff diff.png]
//
// The raw format is the packer's: four 10-bit pixels in five bytes, least significant bits first,
// 2000 bytes a line, RGGB (red first, as the OV02B10 delivers it). YUV files are 4:2:2 packed, two
// bytes a pixel, in the order given (yuyv: Y0 Cb Y1 Cr); -signed reads chroma as two's complement
// around 0 (G2C with no offset), otherwise around 128.
//
// The reference develop is a port of echod's (internal/hardware/camera, camera.go): Full()'s
// bilinear demosaic or I420's one-cell-per-chroma "cell" mode, through tone.tables()' per-channel
// lookup on the 11-bit summed-green scale (black, red/blue gains, white, gamma), then RGB to YCbCr
// with BT.601, full range (the ISP's G2C reset values) or video range (echod's I420).
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"math/rand"
	"os"

	_ "image/jpeg"
)

const (
	W, H = 1600, 1200
	BPL  = W * 10 / 8
)

func die(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: isp2cmp mkraw|ref|show|psnr|tdr|tone|ctl|i420|conv|cmp|dev ...")
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "mkraw":
		mkraw(args)
	case "ref":
		ref(args)
	case "show":
		show(args)
	case "psnr":
		psnr(args)
	case "tdr":
		tdr(args)
	case "tone":
		toneCmd(args)
	case "ctl":
		ctlCmd(args)
	case "i420":
		i420Cmd(args)
	case "conv":
		convCmd(args)
	case "cmp":
		cmpCmd(args)
	case "dev":
		devCmd(args)
	default:
		fmt.Fprintln(os.Stderr, "unknown command", cmd)
		os.Exit(2)
	}
}

// ---- raw ----

func pack(px []uint16) []byte {
	out := make([]byte, BPL*H)
	for y := 0; y < H; y++ {
		l := out[y*BPL:]
		p := px[y*W:]
		for i, j := 0, 0; j+4 <= W; i, j = i+5, j+4 {
			a, b, c, d := p[j], p[j+1], p[j+2], p[j+3]
			l[i] = byte(a)
			l[i+1] = byte(a>>8&3) | byte(b<<2)
			l[i+2] = byte(b>>6&0xf) | byte(c<<4)
			l[i+3] = byte(c>>4&0x3f) | byte(d<<6)
			l[i+4] = byte(d >> 2)
		}
	}
	return out
}

func unpack(raw []byte) []uint16 {
	px := make([]uint16, W*H)
	for y := 0; y < H; y++ {
		l := raw[y*BPL:]
		d := px[y*W:]
		for i, j := 0, 0; j+4 <= W; i, j = i+5, j+4 {
			b0, b1, b2, b3, b4 := uint16(l[i]), uint16(l[i+1]), uint16(l[i+2]), uint16(l[i+3]), uint16(l[i+4])
			d[j] = b0 | (b1&3)<<8
			d[j+1] = b1>>2 | (b2&0xF)<<6
			d[j+2] = b2>>4 | (b3&0x3F)<<4
			d[j+3] = b3>>6 | b4<<2
		}
	}
	return px
}

func srgbToLinear(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

// scene is the synthetic test picture in linear light: the photo (scaled) on the left half, and
// on the right color bars, a gray ramp, a zone plate and fine stripes.
func scene(photo image.Image) func(x, y int) [3]float64 {
	var pb image.Rectangle
	if photo != nil {
		pb = photo.Bounds()
	}
	bars := [][3]float64{{1, 1, 1}, {1, 1, 0}, {0, 1, 1}, {0, 1, 0}, {1, 0, 1}, {1, 0, 0}, {0, 0, 1}, {0.2, 0.2, 0.2}}
	return func(x, y int) [3]float64 {
		if x < W/2 {
			if photo == nil {
				g := float64(x) / float64(W/2)
				return [3]float64{g, g * float64(y) / H, 1 - g}
			}
			// Bilinear scale of the photo to 800x1200 (it is square: crop its middle).
			sx := float64(pb.Dy()) / float64(H)
			fx := float64(pb.Min.X) + (float64(pb.Dx())-float64(W/2)*sx)/2 + (float64(x)+0.5)*sx - 0.5
			fy := float64(pb.Min.Y) + (float64(y)+0.5)*sx - 0.5
			x0, y0 := int(math.Floor(fx)), int(math.Floor(fy))
			ax, ay := fx-float64(x0), fy-float64(y0)
			var out [3]float64
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					px := min(max(x0+dx, pb.Min.X), pb.Max.X-1)
					py := min(max(y0+dy, pb.Min.Y), pb.Max.Y-1)
					r, g, b, _ := photo.At(px, py).RGBA()
					wt := (1 - ax + float64(dx)*(2*ax-1)) * (1 - ay + float64(dy)*(2*ay-1))
					out[0] += wt * srgbToLinear(float64(r)/65535)
					out[1] += wt * srgbToLinear(float64(g)/65535)
					out[2] += wt * srgbToLinear(float64(b)/65535)
				}
			}
			return out
		}
		u := x - W/2
		switch {
		case y < 300: // color bars, at 75%
			c := bars[u*len(bars)/(W/2)]
			return [3]float64{c[0] * 0.75, c[1] * 0.75, c[2] * 0.75}
		case y < 450: // gray ramp, linear
			g := float64(u) / float64(W/2-1)
			return [3]float64{g, g, g}
		case y < 900: // zone plate
			cx, cy := float64(u)-400, float64(y)-675
			r2 := cx*cx + cy*cy
			v := 0.5 + 0.5*math.Cos(r2*math.Pi/1800)
			return [3]float64{v * 0.8, v * 0.8, v * 0.8}
		default: // stripes 1..8 px wide, red/cyan and black/white
			wd := 1 + u/100
			on := (u/wd)%2 == 0
			if y < 1050 {
				if on {
					return [3]float64{0.9, 0.9, 0.9}
				}
				return [3]float64{0.05, 0.05, 0.05}
			}
			if on {
				return [3]float64{0.8, 0.1, 0.1}
			}
			return [3]float64{0.1, 0.6, 0.6}
		}
	}
}

func mkraw(args []string) {
	fs := flag.NewFlagSet("mkraw", flag.ExitOnError)
	out := fs.String("o", "test.raw", "output")
	photoPath := fs.String("photo", "", "picture for the left half")
	black := fs.Int("black", 16, "pedestal, 10-bit")
	wbr := fs.Float64("wbr", 0.55, "sensor response of red relative to green")
	wbb := fs.Float64("wbb", 0.7, "sensor response of blue relative to green")
	truth := fs.String("truth", "", "also write every pixel's red, green and blue (10 bits, 16-bit LE)")
	readNoise := fs.Float64("noise", 0, "read noise, sigma in 10-bit codes")
	shot := fs.Float64("shot", 0, "shot noise: variance per code of signal (0.2: about 5 electrons a code)")
	seed := fs.Int64("seed", 1, "noise seed")
	fs.Parse(args)
	rng := rand.New(rand.NewSource(*seed))
	var photo image.Image
	if *photoPath != "" {
		f, err := os.Open(*photoPath)
		die(err)
		photo, _, err = image.Decode(f)
		die(err)
		f.Close()
	}
	sc := scene(photo)
	px := make([]uint16, W*H)
	span := float64(1023 - *black)
	level := func(v float64) uint16 { return uint16(min(1023, max(0, math.Round(float64(*black)+v*span)))) }
	var lin []byte
	if *truth != "" {
		lin = make([]byte, W*H*6)
	}
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			c := sc(x, y)
			if lin != nil {
				for k, v := range []float64{c[0] * *wbr, c[1], c[2] * *wbb} {
					l := level(v)
					lin[(y*W+x)*6+2*k], lin[(y*W+x)*6+2*k+1] = byte(l), byte(l>>8)
				}
			}
			var v float64
			switch {
			case y%2 == 0 && x%2 == 0:
				v = c[0] * *wbr
			case y%2 == 1 && x%2 == 1:
				v = c[2] * *wbb
			default:
				v = c[1]
			}
			s := v * span
			if *readNoise > 0 || *shot > 0 {
				s += rng.NormFloat64() * math.Sqrt(*readNoise**readNoise+*shot*max(s, 0))
			}
			px[y*W+x] = uint16(min(1023, max(0, math.Round(float64(*black)+s))))
		}
	}
	die(os.WriteFile(*out, pack(px), 0o644))
	if lin != nil {
		die(os.WriteFile(*truth, lin, 0o644))
	}
}

// ---- the CPU develop (echod's) ----

type tone struct {
	gainR, gainB float64
	white, black int
	gamma        float64
}

// tables is echod's tone.tables(): an 11-bit level (a summed green pair, or a single sample doubled)
// to 8 bits, per channel.
func (t tone) tables() *[3][2048]uint8 {
	var lut [3][2048]uint8
	for ch, gain := range []float64{t.gainR, 1, t.gainB} {
		for v := 0; v < 2048; v++ {
			f := float64(v-t.black) * gain / float64(t.white-t.black)
			f = min(max(f, 0), 1)
			lut[ch][v] = uint8(math.Pow(f, t.gamma)*255 + 0.5)
		}
	}
	return &lut
}

// developRGB is echod's Full(): bilinear, edges mirrored, red first.
func developRGB(px []uint16, luts *[3][2048]uint8) []uint8 {
	out := make([]uint8, W*H*3)
	for y := 0; y < H; y++ {
		ym, yp := y-1, y+1
		if ym < 0 {
			ym = 1
		}
		if yp >= H {
			yp = H - 2
		}
		up, cur, dn := px[ym*W:ym*W+W], px[y*W:y*W+W], px[yp*W:yp*W+W]
		o := out[y*W*3:]
		put := func(x int, r, g, b uint32) {
			o[x*3] = luts[0][r<<1]
			o[x*3+1] = luts[1][g<<1]
			o[x*3+2] = luts[2][b<<1]
		}
		for x := 0; x < W; x += 2 {
			xm, xp, xpp := x-1, x+1, x+2
			if xm < 0 {
				xm = 1
			}
			if xpp >= W {
				xpp = W - 2
			}
			c0, c1 := uint32(cur[x]), uint32(cur[xp])
			l0, r1 := uint32(cur[xm]), uint32(cur[xpp])
			u0, u1, d0, d1 := uint32(up[x]), uint32(up[xp]), uint32(dn[x]), uint32(dn[xp])
			um, dm, upp, dpp := uint32(up[xm]), uint32(dn[xm]), uint32(up[xpp]), uint32(dn[xpp])
			if y&1 == 0 {
				put(x, c0, (l0+c1+u0+d0)/4, (um+u1+dm+d1)/4)
				put(xp, (c0+r1)/2, c1, (u1+d1)/2)
			} else {
				put(x, (u0+d0)/2, c0, (l0+c1)/2)
				put(xp, (u0+upp+d0+dpp)/4, (c0+r1+u1+d1)/4, c1)
			}
		}
	}
	return out
}

// developCell is the I420 path's color: each 2x2 cell's red, summed greens and blue for all four
// pixels' chroma, each pixel's own sample (with the cell's other colors) for its luma; here as RGB
// per pixel, the luma site's own green or the cell's.
func developCell(px []uint16, luts *[3][2048]uint8) []uint8 {
	out := make([]uint8, W*H*3)
	for cy := 0; cy < H/2; cy++ {
		for cx := 0; cx < W/2; cx++ {
			r := px[2*cy*W+2*cx]
			g0, g1 := px[2*cy*W+2*cx+1], px[(2*cy+1)*W+2*cx]
			b := px[(2*cy+1)*W+2*cx+1]
			R, G, B := luts[0][(r<<1)&2047], luts[1][(g0+g1)&2047], luts[2][(b<<1)&2047]
			for _, p := range [][3]int{{0, 0, -1}, {1, 0, int(g0)}, {0, 1, int(g1)}, {1, 1, -1}} {
				g := G
				if p[2] >= 0 {
					g = luts[1][(p[2]<<1)&2047]
				}
				i := ((2*cy+p[1])*W + 2*cx + p[0]) * 3
				out[i], out[i+1], out[i+2] = R, g, B
			}
		}
	}
	return out
}

func clamp8(v float64) uint8 { return uint8(min(max(math.Round(v), 0), 255)) }

// toYUYV: BT.601, full range (G2C's reset coefficients, chroma around 128 here) or video range;
// 4:2:2 by averaging each pixel pair's chroma.
func toYUYV(rgb []uint8, limited bool) []uint8 {
	out := make([]uint8, W*H*2)
	for i := 0; i < W*H; i += 2 {
		var y [2]float64
		var cb, cr float64
		for k := 0; k < 2; k++ {
			r, g, b := float64(rgb[(i+k)*3]), float64(rgb[(i+k)*3+1]), float64(rgb[(i+k)*3+2])
			if limited {
				y[k] = 16 + (65.481*r+128.553*g+24.966*b)/255
				cb += 128 + (-37.797*r-74.203*g+112*b)/255
				cr += 128 + (112*r-93.786*g-18.214*b)/255
			} else {
				y[k] = 0.299*r + 0.587*g + 0.114*b
				cb += 128 - 0.168736*r - 0.331264*g + 0.5*b
				cr += 128 + 0.5*r - 0.418688*g - 0.081312*b
			}
		}
		o := out[i*2:]
		o[0], o[1], o[2], o[3] = clamp8(y[0]), clamp8(cb/2), clamp8(y[1]), clamp8(cr/2)
	}
	return out
}

func ref(args []string) {
	fs := flag.NewFlagSet("ref", flag.ExitOnError)
	rawPath := fs.String("raw", "", "packed RAW10 input")
	out := fs.String("o", "ref.yuv", "YUYV output")
	pngPath := fs.String("png", "", "also write the RGB picture")
	t := tone{}
	fs.IntVar(&t.black, "black", 0, "black, 11-bit summed-green scale")
	fs.IntVar(&t.white, "white", 2046, "white, same scale")
	fs.Float64Var(&t.gainR, "gr", 1, "red gain")
	fs.Float64Var(&t.gainB, "gb", 1, "blue gain")
	fs.Float64Var(&t.gamma, "gamma", 1, "output gamma (echod: 1/1.8)")
	mode := fs.String("demosaic", "bilinear", "bilinear (Full) or cell (I420)")
	rng := fs.String("range", "full", "full or limited")
	fs.Parse(args)
	raw, err := os.ReadFile(*rawPath)
	die(err)
	px := unpack(raw)
	luts := t.tables()
	var rgb []uint8
	if *mode == "cell" {
		rgb = developCell(px, luts)
	} else {
		rgb = developRGB(px, luts)
	}
	die(os.WriteFile(*out, toYUYV(rgb, *rng == "limited"), 0o644))
	if *pngPath != "" {
		img := image.NewRGBA(image.Rect(0, 0, W, H))
		for i := 0; i < W*H; i++ {
			copy(img.Pix[i*4:], rgb[i*3:i*3+3])
			img.Pix[i*4+3] = 255
		}
		writePNG(*pngPath, img)
	}
}

// ---- the ISP's output ----

type yuvOpts struct {
	w, h, stride int
	order        string
	signed       bool
}

func (o *yuvOpts) flags(fs *flag.FlagSet) {
	fs.IntVar(&o.w, "w", W, "width")
	fs.IntVar(&o.h, "h", H, "height")
	fs.IntVar(&o.stride, "stride", W*2, "bytes a line")
	fs.StringVar(&o.order, "order", "yuyv", "yuyv, uyvy, yvyu or vyuy")
	fs.BoolVar(&o.signed, "signed", false, "chroma is two's complement around 0")
}

// planes reads a 4:2:2 file into Y (w*h) and Cb, Cr (w/2*h), chroma moved to around 128.
func (o *yuvOpts) planes(path string) (y, cb, cr []uint8) {
	d, err := os.ReadFile(path)
	die(err)
	if len(d) < o.stride*(o.h-1)+o.w*2 {
		die(fmt.Errorf("%s: %d bytes, short", path, len(d)))
	}
	pos := map[string][4]int{"yuyv": {0, 2, 1, 3}, "uyvy": {1, 3, 0, 2}, "yvyu": {0, 2, 3, 1}, "vyuy": {1, 3, 2, 0}}[o.order]
	y, cb, cr = make([]uint8, o.w*o.h), make([]uint8, o.w/2*o.h), make([]uint8, o.w/2*o.h)
	for r := 0; r < o.h; r++ {
		l := d[r*o.stride:]
		for x := 0; x < o.w/2; x++ {
			g := l[x*4 : x*4+4]
			y[r*o.w+2*x], y[r*o.w+2*x+1] = g[pos[0]], g[pos[1]]
			u, v := g[pos[2]], g[pos[3]]
			if o.signed {
				u, v = u+128, v+128
			}
			cb[r*o.w/2+x], cr[r*o.w/2+x] = u, v
		}
	}
	return
}

func toRGBA(y, cb, cr []uint8, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for r := 0; r < h; r++ {
		for x := 0; x < w; x++ {
			c := color.YCbCr{Y: y[r*w+x], Cb: cb[r*w/2+x/2], Cr: cr[r*w/2+x/2]}
			R, G, B := color.YCbCrToRGB(c.Y, c.Cb, c.Cr)
			i := r*img.Stride + x*4
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = R, G, B, 255
		}
	}
	return img
}

func writePNG(path string, img image.Image) {
	f, err := os.Create(path)
	die(err)
	die(png.Encode(f, img))
	die(f.Close())
}

func show(args []string) {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	var o yuvOpts
	o.flags(fs)
	in := fs.String("yuv", "", "input")
	out := fs.String("o", "out.png", "PNG")
	fs.Parse(args)
	y, cb, cr := o.planes(*in)
	writePNG(*out, toRGBA(y, cb, cr, o.w, o.h))
}

func planePSNR(a, b []uint8, w, h, x0, x1 int) (psnr, mean float64) {
	var se, sd float64
	n := 0
	for r := 0; r < h; r++ {
		for x := x0; x < x1; x++ {
			d := float64(a[r*w+x]) - float64(b[r*w+x])
			se += d * d
			sd += d
			n++
		}
	}
	mse := se / float64(n)
	if mse == 0 {
		return math.Inf(1), 0
	}
	return 10 * math.Log10(255*255/mse), sd / float64(n)
}

func psnr(args []string) {
	fs := flag.NewFlagSet("psnr", flag.ExitOnError)
	var o yuvOpts
	o.flags(fs)
	a := fs.String("a", "", "reference YUYV (1600x1200, stride 3200, chroma around 128)")
	b := fs.String("b", "", "ISP output")
	x0 := fs.Int("x0", 0, "first column compared")
	x1 := fs.Int("x1", W, "column past the last compared")
	diff := fs.String("diff", "", "write |Y difference| x8 as a PNG")
	fs.Parse(args)
	ro := yuvOpts{w: o.w, h: o.h, stride: W * 2, order: "yuyv"}
	ay, acb, acr := ro.planes(*a)
	by, bcb, bcr := o.planes(*b)
	py, my := planePSNR(ay, by, o.w, o.h, *x0, *x1)
	pu, mu := planePSNR(acb, bcb, o.w/2, o.h, *x0/2, *x1/2)
	pv, mv := planePSNR(acr, bcr, o.w/2, o.h, *x0/2, *x1/2)
	fmt.Printf("PSNR Y %.2f dB (mean a-b %+.2f)  Cb %.2f dB (%+.2f)  Cr %.2f dB (%+.2f)\n", py, my, pu, mu, pv, mv)
	if *diff != "" {
		img := image.NewGray(image.Rect(0, 0, o.w, o.h))
		for i := range ay {
			d := int(ay[i]) - int(by[i])
			if d < 0 {
				d = -d
			}
			img.Pix[i] = uint8(min(d*8, 255))
		}
		writePNG(*diff, img)
	}
}

// ---- the tile table ----

// tile is one column of the frame: the input pixels it reads and the output pixels it writes.
type tile struct{ inXS, inXE, outXS, outXE int }

// Tile geometry for RAW10 -> UNP -> CFA -> G2C -> C42 -> IMG2O, as MediaTek's tile driver
// (libdpframework's tpipe_main_platform) lays it out: tiles at most 768 input pixels wide (the
// line buffers), cut from the right edge so the leftover tile comes first; an inner tile edge
// costs 6 input pixels on its left and 4 on its right (the demosaic's reach, the left one kept
// even for 4:2:2).
const (
	tileMax    = 768
	marginLeft = 6
	marginRgt  = 4
)

func tiles(w int) []tile {
	var ts []tile
	end := w - 1 // input end of the tile being placed
	outEnd := w - 1
	for {
		start := end - tileMax + 1
		if start <= 0 {
			ts = append([]tile{{0, end, 0, outEnd}}, ts...)
			return ts
		}
		outStart := start + marginLeft
		ts = append([]tile{{start, end, outStart, outEnd}}, ts...)
		outEnd = outStart - 1
		end = outEnd + marginRgt
	}
}

// tdrWords is the table: per tile CTRL, IMGI offset, IMGI size, IMG2O offset, IMG2O size, UNP,
// with the field layout tile_tdr_isp_fprint_dump prints. IMGI reads 16-bit aligned: the offset is
// rounded down to an even byte, UNP's start bit is where the first pixel starts in it and its end
// bit where the last one ends in the last 16-bit word read.
//
// pixID is the color of the first pixel (MediaTek's order: 0 B, 1 Gb, 2 Gr, 3 R; the OV02B10's
// is red, 3). lastOnly puts LAST_IRQ on the last tile only, so one start runs the whole table;
// the tile driver sets it on every tile (its irq mode 2), and the hardware then stops after each.
func tdrWords(w, h, pixID int, lastOnly bool) []uint32 {
	var out []uint32
	ts := tiles(w)
	for i, t := range ts {
		edge := uint32(0b1100) // top, bottom
		if t.inXS == 0 {
			edge |= 1
		}
		if t.inXE == w-1 {
			edge |= 2
		}
		ctrl := edge | uint32(pixID&0xf)<<8 | 1<<19 // PIX_ID, CTRL_IMG2O_EN
		if !lastOnly || i == len(ts)-1 {
			ctrl |= 1 << 5 // LAST_IRQ
		}
		startBits := t.inXS * 10
		off := startBits / 16 * 2
		endBits := (t.inXE+1)*10 - off*8
		nbytes := (endBits + 15) / 16 * 2
		out = append(out, ctrl, uint32(off), uint32(nbytes-1)|uint32(h-1)<<16,
			uint32(t.outXS*2), uint32((t.outXE-t.outXS+1)*2-1)|uint32(h-1)<<16,
			uint32(startBits-off*8)|uint32(endBits%16)<<16)
	}
	return out
}

func tdr(args []string) {
	fs := flag.NewFlagSet("tdr", flag.ExitOnError)
	out := fs.String("o", "tdr.bin", "output, little-endian words")
	w := fs.Int("w", W, "width")
	h := fs.Int("h", H, "height")
	check := fs.String("check", "", "compare with a table from the tile driver")
	pixID := fs.Int("pixid", 3, "first pixel: 0 B, 1 Gb, 2 Gr, 3 R")
	lastOnly := fs.Bool("last-only", true, "LAST_IRQ on the last tile only")
	fs.Parse(args)
	words := tdrWords(*w, *h, *pixID, *lastOnly)
	b := make([]byte, 4*len(words))
	for i, v := range words {
		b[4*i], b[4*i+1], b[4*i+2], b[4*i+3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
	}
	for i, t := range tiles(*w) {
		fmt.Printf("tile %d: in %d..%d out %d..%d: %08x\n", i, t.inXS, t.inXE, t.outXS, t.outXE, words[6*i:6*i+6])
	}
	die(os.WriteFile(*out, b, 0o644))
	if *check != "" {
		lib, err := os.ReadFile(*check)
		die(err)
		if string(lib) != string(b) {
			die(fmt.Errorf("differs from %s", *check))
		}
		fmt.Println("identical to", *check)
	}
}
