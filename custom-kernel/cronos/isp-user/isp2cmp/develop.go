package main

// Stage 3: the frame's tone as echod measures it, the ISP registers that apply it, echod's I420
// develop (the CPU path the stream falls back to), the 4:2:2 -> 4:2:0 step the ISP's output takes,
// and a comparison of I420 pictures, against each other or against the scene a synthetic frame was
// made from.
//
//	isp2cmp tone  -raw f.raw                       echod's tone for the frame (stats, camera.go)
//	isp2cmp ctl   -raw f.raw [-o ctl.txt]          isp2test commands programming that tone
//	isp2cmp i420  -raw f.raw -o cpu.i420 [-truth syn.lin]
//	                                               echod's I420 (or the scene's, -truth, with the
//	                                               frame's own tone)
//	isp2cmp conv  -yuv isp.yuyv -o isp.i420        the ISP's YUYV (signed chroma) to I420
//	isp2cmp cmp   -a a.i420 -b b.i420 [-x0 -x1] [-png a.png -pngb b.png]
//	                                               PSNR and mean difference per plane

import (
	"flag"
	"fmt"
	"image"
	"math"
	"os"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// ---- echod's tone (camera.go sampleWith, exposure.go gammaFor/blackFor), for the OV02B10 ----

const (
	statsStep   = 2
	gammaNormal = 1 / 1.8
	hdrRatio    = 3.0
	hdrStrength = 0.45
	gammaMaxAdd = 0.9
	blackCap    = 64
	pedestal    = 0
)

func gammaFor(median, top int) float64 {
	r := float64(top) / math.Max(float64(median), 1)
	if r <= hdrRatio {
		return gammaNormal
	}
	add := math.Min(hdrStrength*math.Log2(r/hdrRatio), gammaMaxAdd)
	return 1 / (1.8 + add)
}

func blackFor(low, white, ped int) int {
	return ped + max(0, min(low-ped, blackCap, (white-ped)/4))
}

// stats is echod's sampleWith at step 2 (even: straight from the packed lines).
func stats(raw []byte) tone {
	step := statsStep
	cellsW, cellsH := W/2, H/2
	var sumR, sumG, sumB uint64
	var hist, center [2048]int
	taken, middle := 0, 0
	for y := 0; y < cellsH; y += step {
		l0, l1 := raw[(2*y)*BPL:(2*y+1)*BPL], raw[(2*y+1)*BPL:(2*y+2)*BPL]
		down := y >= cellsH/4 && y < cellsH*3/4
		for x := 0; x < cellsW; x += step {
			i := x / 2 * 5
			a0, a1, a2 := uint16(l0[i]), uint16(l0[i+1]), uint16(l0[i+2])
			b0, b1, b2 := uint16(l1[i]), uint16(l1[i+1]), uint16(l1[i+2])
			r, b := a0|(a1&3)<<8, b1>>2|(b2&0xF)<<6
			g := (a1>>2 | (a2&0xF)<<6) + (b0 | (b1&3)<<8)
			sumR += uint64(r)
			sumG += uint64(g)
			sumB += uint64(b)
			hist[g]++
			taken++
			if down && x >= cellsW/4 && x < cellsW*3/4 {
				center[g]++
				middle++
			}
		}
	}
	n := float64(taken)
	t := tone{gainR: 1, gainB: 1, white: 1, gamma: gammaNormal}
	ped := float64(pedestal)
	avgR, avgG, avgB := float64(sumR)*2/n-ped, float64(sumG)/n-ped, float64(sumB)*2/n-ped
	if avgR > 1 && avgB > 1 && avgG > 1 {
		t.gainR = math.Round(avgG/avgR*256) / 256
		t.gainB = math.Round(avgG/avgB*256) / 256
	}
	seen, low := 0, -1
	for v := 0; v < len(hist); v++ {
		seen += hist[v]
		if low < 0 && seen >= taken/1000 {
			low = v
		}
		if seen >= taken*99/100 {
			t.white = v
			break
		}
	}
	median, half := 0, middle/2
	for v, seen := 0, 0; v < len(center); v++ {
		if seen += center[v]; seen >= half {
			median = v
			break
		}
	}
	if t.white < pedestal+32 {
		t.white = pedestal + 32
	}
	t.gamma = gammaFor(median-pedestal, t.white-pedestal)
	t.black = blackFor(low, t.white, pedestal)
	return t
}

func (t tone) String() string {
	return fmt.Sprintf("black %d white %d gainR %.4f gainB %.4f gamma 1/%.3f", t.black, t.white, t.gainR, t.gainB, 1/t.gamma)
}

func toneFlags(fs *flag.FlagSet) (*string, *tone, *bool) {
	raw := fs.String("raw", "", "packed RAW10 input")
	t := &tone{}
	fs.IntVar(&t.black, "black", -1, "override: black, 11-bit summed-green scale")
	fs.IntVar(&t.white, "white", -1, "override: white")
	fs.Float64Var(&t.gainR, "gr", 0, "override: red gain")
	fs.Float64Var(&t.gainB, "gb", 0, "override: blue gain")
	fs.Float64Var(&t.gamma, "gamma", 0, "override: gamma (echod: 1/1.8)")
	linear := fs.Bool("linear", false, "linear tone: black 0, gains 1, white 2046, gamma 1")
	return raw, t, linear
}

// resolve is the frame's own tone with any overrides.
func resolve(raw []byte, o *tone, linear bool) tone {
	t := stats(raw)
	if linear {
		t = tone{gainR: 1, gainB: 1, white: 2046, gamma: 1}
	}
	if o.black >= 0 {
		t.black = o.black
	}
	if o.white >= 0 {
		t.white = o.white
	}
	if o.gainR > 0 {
		t.gainR = o.gainR
	}
	if o.gainB > 0 {
		t.gainB = o.gainB
	}
	if o.gamma > 0 {
		t.gamma = o.gamma
	}
	return t
}

func toneCmd(args []string) {
	fs := flag.NewFlagSet("tone", flag.ExitOnError)
	rawPath, o, linear := toneFlags(fs)
	fs.Parse(args)
	raw, err := os.ReadFile(*rawPath)
	die(err)
	fmt.Println(resolve(raw, o, *linear))
}

// ---- the ISP's tone registers ----

// ispTone is what the kernel takes (uapi cronos-camera.h, struct cronos_isp_tone): a black offset
// added to every sample on the ISP's 12-bit scale (a 10-bit sample times 4), the gain of each Bayer
// channel in 1/512 (red, the two greens, blue) applied after it, and the output curve, 10 bits at
// the 145 knots of the gamma table (inputs 0, 8, ..., 512, 528, ..., 1024, 1056, ..., 2048, 2176,
// ..., 4096).
type ispTone struct {
	offset int
	gain   [4]int // R, Gr, Gb, B
	curve  [145]int
}

// knot is the 12-bit input of the gamma table's entry i (i = 144 is the end of the range).
func knot(i int) int {
	switch {
	case i < 64:
		return 8 * i
	case i < 96:
		return 512 + 16*(i-64)
	case i < 128:
		return 1024 + 32*(i-96)
	}
	return 2048 + 128*(i-128)
}

// ispToneFor maps echod's tone onto the ISP: echod's level of a channel is
// clamp((2x - black) * gain / (white - black)) on the 11-bit scale (x the 10-bit sample), then
// ^gamma. On the 12-bit scale X = 4x that is (X - 2 black) * gain * 2048/(white - black) / 4096:
// the offset is -2 black, the gain carries the stretch, and the curve is the gamma alone.
func ispToneFor(t tone) ispTone {
	var it ispTone
	it.offset = -2 * t.black
	span := float64(max(t.white-t.black, 1))
	for c, g := range []float64{t.gainR, 1, 1, t.gainB} {
		it.gain[c] = int(math.Round(g * 2048 / span * 512))
	}
	for i := range it.curve {
		f := float64(knot(i)) / 4096
		it.curve[i] = int(math.Round(math.Pow(f, t.gamma) * 1020))
	}
	return it
}

// registers is the hardware form, as the driver writes it: OBC offsets and gains (in the ISP's
// channel order B, Gb, Gr, R; gains 13 bits, Q9), the PGN gains taking whatever OBC's 16x cannot,
// and the gamma tables, each entry its output (10 bits) and the rise to the next (6 bits, at most 63).
type registers struct {
	obcOfs, obcGain, pgnGain [4]int
	pgn                      bool
	ggm                      [144]uint32
}

func (it ispTone) registers() registers {
	var r registers
	order := [4]int{3, 2, 1, 0} // ISP channel i takes ispTone channel order[i]: B, Gb, Gr, R
	for i, c := range order {
		r.obcOfs[i] = it.offset & 0x1fff
		g := it.gain[c]
		if g <= 8191 {
			r.obcGain[i], r.pgnGain[i] = g, 512
		} else {
			r.obcGain[i] = 4096
			r.pgnGain[i] = min((g+4)/8, 8191)
			r.pgn = true
		}
	}
	for i := range r.ggm {
		v := min(max(it.curve[i], 0), 1023)
		next := min(max(it.curve[i+1], 0), 1023)
		// The rise is 6 bits. Only the first entry of a steep curve (gamma 1/2 and steeper) rises
		// more: start it higher instead, which lifts only what is at or below black.
		v = max(v, next-63)
		r.ggm[i] = uint32(v) | uint32(min(max(next-v, 0), 63))<<10
	}
	return r
}

// g2c is BT.601 video range in Q9, echod's integer coefficients doubled: Y 16 + (66 R + 129 G +
// 25 B)/256, Cb (-38 R - 74 G + 112 B)/256, Cr (112 R - 94 G - 18 B)/256; the luma offset in
// quarters of a code, the chroma left signed (around 0).
var g2c = [3][3]int{{132, 258, 50}, {-76, -148, 224}, {224, -188, -36}}

const g2cYOffset = 16 * 4

func ctlCmd(args []string) {
	fs := flag.NewFlagSet("ctl", flag.ExitOnError)
	rawPath, o, linear := toneFlags(fs)
	out := fs.String("o", "", "write here (default stdout)")
	fs.Parse(args)
	raw, err := os.ReadFile(*rawPath)
	die(err)
	t := resolve(raw, o, *linear)
	r := ispToneFor(t).registers()
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", t)
	en1 := 0x00300008
	if r.pgn {
		en1 |= 1 << 11
	}
	fmt.Fprintf(&b, "w 0x004 %#x\n", en1)
	fmt.Fprintf(&b, "w 0x500 %d %d %d %d %d %d %d %d\n", r.obcOfs[0], r.obcOfs[1], r.obcOfs[2], r.obcOfs[3],
		r.obcGain[0], r.obcGain[1], r.obcGain[2], r.obcGain[3])
	fmt.Fprintf(&b, "w 0x888 %#x %#x\n", r.pgnGain[0]|r.pgnGain[1]<<16, r.pgnGain[2]|r.pgnGain[3]<<16)
	for k := 0; k < 144; k += 10 {
		var rb, g []string
		for i := k; i < min(k+10, 144); i++ {
			rb = append(rb, fmt.Sprintf("%#x", r.ggm[i]|r.ggm[i]<<16))
			g = append(g, fmt.Sprintf("%#x", r.ggm[i]))
		}
		fmt.Fprintf(&b, "w %#x %s\n", 0x1000+4*k, strings.Join(rb, " "))
		fmt.Fprintf(&b, "w %#x %s\n", 0x1300+4*k, strings.Join(g, " "))
	}
	b.WriteString("w 0x1600 1\n")
	c := func(a, b int) uint32 { return uint32(a)&0x7ff | (uint32(b)&0x7ff)<<16 }
	fmt.Fprintf(&b, "w 0xa00 %#x %#x %#x %#x %#x %#x\n", c(g2c[0][0], g2c[0][1]), c(g2c[0][2], g2cYOffset),
		c(g2c[1][0], g2c[1][1]), c(g2c[1][2], 0), c(g2c[2][0], g2c[2][1]), c(g2c[2][2], 0))
	if *out == "" {
		fmt.Print(b.String())
		return
	}
	die(os.WriteFile(*out, []byte(b.String()), 0o644))
}

// ---- echod's I420 (camera.go i420Rows, red first) ----

func i420Rows(raw []byte, lut *[3][2048]uint8) []byte {
	w, h := W, H
	dst := make([]byte, w*h*3/2)
	yp, cb, cr := dst[:w*h], dst[w*h:w*h+w*h/4], dst[w*h+w*h/4:]
	n := w / 4 * 5
	lt0, lt1, lt2 := &lut[0], &lut[1], &lut[2]
	for cy := 0; cy < h/2; cy++ {
		l0 := raw[2*cy*BPL:][:n]
		l1 := raw[(2*cy+1)*BPL:][:n]
		y0 := yp[2*cy*w:][:w]
		y1 := yp[(2*cy+1)*w:][:w]
		u := cb[cy*(w/2):][:w/2]
		v := cr[cy*(w/2):][:w/2]
		for len(l0) >= 5 {
			a0, a1, a2, a3, a4 := uint(l0[0]), uint(l0[1]), uint(l0[2]), uint(l0[3]), uint(l0[4])
			b0, b1, b2, b3, b4 := uint(l1[0]), uint(l1[1]), uint(l1[2]), uint(l1[3]), uint(l1[4])
			p := [4]uint{a0 | (a1&3)<<8, a1>>2 | (a2&0xF)<<6, a2>>4 | (a3&0x3F)<<4, a3>>6 | a4<<2}
			q := [4]uint{b0 | (b1&3)<<8, b1>>2 | (b2&0xF)<<6, b2>>4 | (b3&0x3F)<<4, b3>>6 | b4<<2}
			for k := 0; k < 2; k++ {
				r, g0, g1, bl := p[2*k], p[2*k+1], q[2*k], q[2*k+1]
				R, B := int(lt0[(r<<1)&2047]), int(lt2[(bl<<1)&2047])
				G := int(lt1[(g0+g1)&2047])
				rb := 66*R + 25*B + 128
				yc := uint8((rb+129*G)>>8 + 16)
				y0[2*k], y0[2*k+1] = yc, uint8((rb+129*int(lt1[(g0<<1)&2047]))>>8+16)
				y1[2*k], y1[2*k+1] = uint8((rb+129*int(lt1[(g1<<1)&2047]))>>8+16), yc
				u[k] = uint8((-38*R-74*G+112*B+128)>>8 + 128)
				v[k] = uint8((112*R-94*G-18*B+128)>>8 + 128)
			}
			l0, l1, y0, y1, u, v = l0[5:], l1[5:], y0[4:], y1[4:], u[2:], v[2:]
		}
	}
	return dst
}

// truthI420 develops the scene itself (mkraw -truth: every pixel's red, green and blue as the sensor
// would have read them, 10 bits) with the frame's tone: what a perfect demosaic would give.
func truthI420(lin []uint16, lut *[3][2048]uint8) []byte {
	w, h := W, H
	dst := make([]byte, w*h*3/2)
	yp, cb, cr := dst[:w*h], dst[w*h:w*h+w*h/4], dst[w*h+w*h/4:]
	for y := 0; y < h; y += 2 {
		for x := 0; x < w; x += 2 {
			var su, sv int
			for _, d := range [][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
				i := ((y+d[1])*w + x + d[0]) * 3
				R := int(lut[0][(lin[i]<<1)&2047])
				G := int(lut[1][(lin[i+1]<<1)&2047])
				B := int(lut[2][(lin[i+2]<<1)&2047])
				yp[(y+d[1])*w+x+d[0]] = uint8((66*R+129*G+25*B+128)>>8 + 16)
				su += -38*R - 74*G + 112*B
				sv += 112*R - 94*G - 18*B
			}
			cb[y/2*(w/2)+x/2] = uint8((su+512)>>10 + 128)
			cr[y/2*(w/2)+x/2] = uint8((sv+512)>>10 + 128)
		}
	}
	return dst
}

func i420Cmd(args []string) {
	fs := flag.NewFlagSet("i420", flag.ExitOnError)
	rawPath, o, linear := toneFlags(fs)
	out := fs.String("o", "cpu.i420", "output")
	truth := fs.String("truth", "", "develop this scene (mkraw -truth) instead, with the frame's tone")
	fs.Parse(args)
	raw, err := os.ReadFile(*rawPath)
	die(err)
	t := resolve(raw, o, *linear)
	fmt.Println(t)
	lut := t.tables()
	if *truth != "" {
		d, err := os.ReadFile(*truth)
		die(err)
		lin := make([]uint16, len(d)/2)
		for i := range lin {
			lin[i] = uint16(d[2*i]) | uint16(d[2*i+1])<<8
		}
		die(os.WriteFile(*out, truthI420(lin, lut), 0o644))
		return
	}
	die(os.WriteFile(*out, i420Rows(raw, lut), 0o644))
}

// yuyvToI420 is the CPU's part of the ISP develop: the luma as it is, the chroma of each two lines
// averaged (rounded) and moved from two's complement to around 128.
func yuyvToI420(d []byte, stride int, signed bool) []byte {
	bias := 128
	if !signed {
		bias = 0
	}
	w, h := W, H
	dst := make([]byte, w*h*3/2)
	yp, cb, cr := dst[:w*h], dst[w*h:w*h+w*h/4], dst[w*h+w*h/4:]
	for y := 0; y < h; y += 2 {
		a, b := d[y*stride:], d[(y+1)*stride:]
		for x := 0; x < w/2; x++ {
			yp[y*w+2*x], yp[y*w+2*x+1] = a[4*x], a[4*x+2]
			yp[(y+1)*w+2*x], yp[(y+1)*w+2*x+1] = b[4*x], b[4*x+2]
			ch := func(p byte) int {
				if signed {
					return int(int8(p))
				}
				return int(p)
			}
			u := (ch(a[4*x+1]) + ch(b[4*x+1]) + 1) >> 1
			v := (ch(a[4*x+3]) + ch(b[4*x+3]) + 1) >> 1
			cb[y/2*(w/2)+x], cr[y/2*(w/2)+x] = uint8(u+bias), uint8(v+bias)
		}
	}
	return dst
}

func convCmd(args []string) {
	fs := flag.NewFlagSet("conv", flag.ExitOnError)
	in := fs.String("yuv", "", "the ISP's YUYV")
	out := fs.String("o", "isp.i420", "output")
	stride := fs.Int("stride", W*2, "bytes a line")
	signed := fs.Bool("signed", true, "chroma in two's complement (the ISP's); false: around 128 (ref's)")
	fs.Parse(args)
	d, err := os.ReadFile(*in)
	die(err)
	die(os.WriteFile(*out, yuyvToI420(d, *stride, *signed), 0o644))
}

func i420Planes(path string) (y, u, v []uint8) {
	d, err := os.ReadFile(path)
	die(err)
	if len(d) < W*H*3/2 {
		die(fmt.Errorf("%s: %d bytes, short", path, len(d)))
	}
	return d[:W*H], d[W*H : W*H+W*H/4], d[W*H+W*H/4 : W*H*3/2]
}

func i420PNG(path string, y, u, v []uint8) {
	cb, cr := make([]uint8, W/2*H), make([]uint8, W/2*H)
	for r := 0; r < H; r++ {
		copy(cb[r*W/2:], u[r/2*W/2:r/2*W/2+W/2])
		copy(cr[r*W/2:], v[r/2*W/2:r/2*W/2+W/2])
	}
	// toRGBA treats the luma as full range; stretch video range first, for the eye.
	yy := make([]uint8, len(y))
	for i, c := range y {
		yy[i] = clamp8((float64(c) - 16) * 255 / 219)
	}
	for i := range cb {
		cb[i] = clamp8((float64(cb[i])-128)*255/224 + 128)
		cr[i] = clamp8((float64(cr[i])-128)*255/224 + 128)
	}
	writePNG(path, toRGBA(yy, cb, cr, W, H))
}

func cmpCmd(args []string) {
	fs := flag.NewFlagSet("cmp", flag.ExitOnError)
	a := fs.String("a", "", "reference I420")
	b := fs.String("b", "", "I420 compared")
	x0 := fs.Int("x0", 0, "first column compared")
	x1 := fs.Int("x1", W, "column past the last")
	y0 := fs.Int("y0", 0, "first row compared")
	y1 := fs.Int("y1", H, "row past the last")
	pa := fs.String("png", "", "write a as a PNG")
	pb := fs.String("pngb", "", "write b as a PNG")
	diff := fs.String("diff", "", "write |Y difference| x8 as a PNG")
	fs.Parse(args)
	ay, au, av := i420Planes(*a)
	by, bu, bv := i420Planes(*b)
	region := func(p, q []uint8, w, sx0, sx1, sy0, sy1 int) (float64, float64) {
		var se, sd float64
		n := 0
		for r := sy0; r < sy1; r++ {
			for x := sx0; x < sx1; x++ {
				d := float64(p[r*w+x]) - float64(q[r*w+x])
				se += d * d
				sd += d
				n++
			}
		}
		if se == 0 {
			return math.Inf(1), 0
		}
		return 10 * math.Log10(255*255*float64(n)/se), sd / float64(n)
	}
	py, my := region(ay, by, W, *x0, *x1, *y0, *y1)
	pu, mu := region(au, bu, W/2, *x0/2, *x1/2, *y0/2, *y1/2)
	pv, mv := region(av, bv, W/2, *x0/2, *x1/2, *y0/2, *y1/2)
	fmt.Printf("PSNR Y %.2f dB (mean a-b %+.2f)  Cb %.2f dB (%+.2f)  Cr %.2f dB (%+.2f)\n", py, my, pu, mu, pv, mv)
	if *pa != "" {
		i420PNG(*pa, ay, au, av)
	}
	if *pb != "" {
		i420PNG(*pb, by, bu, bv)
	}
	if *diff != "" {
		img := image.NewGray(image.Rect(0, 0, W, H))
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

// ---- the driver's CRONOS_IOC_DEVELOP (patches/mt8163-cronos-camera.c), on the unit ----

type cronosDevelop struct {
	raw, dst         uint64
	rawSize, dstSize uint32
	black            int32
	gain             [4]uint32
	curve            [145]uint16
	_                uint16
	flags            uint32
	hwUs             uint32
}

func devCmd(args []string) {
	fs := flag.NewFlagSet("dev", flag.ExitOnError)
	rawPath, o, linear := toneFlags(fs)
	out := fs.String("o", "dev.i420", "output")
	node := fs.String("dev", "/dev/video1", "the camera's node")
	n := fs.Int("n", 1, "developments (timed)")
	fs.Parse(args)
	raw, err := os.ReadFile(*rawPath)
	die(err)
	t := resolve(raw, o, *linear)
	it := ispToneFor(t)
	var d cronosDevelop
	dst := make([]byte, W*H*3/2)
	d.raw, d.rawSize = uint64(uintptr(unsafe.Pointer(&raw[0]))), uint32(len(raw))
	d.dst, d.dstSize = uint64(uintptr(unsafe.Pointer(&dst[0]))), uint32(len(dst))
	d.black = int32(it.offset)
	for c := range it.gain {
		d.gain[c] = uint32(it.gain[c])
	}
	for i := range it.curve {
		d.curve[i] = uint16(it.curve[i])
	}
	fd, err := syscall.Open(*node, syscall.O_RDWR, 0)
	die(err)
	req := uintptr(3<<30 | unsafe.Sizeof(d)<<16 | 'V'<<8 | 192)
	var total time.Duration
	for i := 0; i < *n; i++ {
		began := time.Now()
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(unsafe.Pointer(&d))); e != 0 {
			die(fmt.Errorf("develop %d: %v", i, e))
		}
		total += time.Since(began)
	}
	runtime.KeepAlive(raw)
	runtime.KeepAlive(dst)
	fmt.Printf("%s; ISP %d us, call %.2f ms on average\n", t, d.hwUs, total.Seconds()*1000/float64(*n))
	die(os.WriteFile(*out, dst, 0o644))
}
