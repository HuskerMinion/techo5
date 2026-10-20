//go:build linux && hwdevice

package camstream

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/vpu"
)

// On a Show 8 with the VPU driver: the stock noise reduction (vpu.Chroma, swnr) on live 1456x1088
// frames of a still scene, against the scene's mean over HW_FRAMES (24) frames as the reference: the
// mean of N frames keeps the picture and leaves 1/√N of the noise, so a frame's error against it is
// its noise (and whatever the filter takes of the picture). Per plane: the error over the whole
// picture as PSNR, the error in the flattest and the busiest eighth of the 16x16 blocks (noise, and
// detail lost), before and after each tuning in HW_NR ("K,S,BW,BT;..."). HW_MAXSHUT caps the
// exposure to force the gain up; HW_DUMP gets crops as PNG (scratch only).
func TestDeviceNRQuality(t *testing.T) {
	if !vpu.Available() {
		t.Skip("no VPU")
	}
	n := 24
	if v, err := strconv.Atoi(os.Getenv("HW_FRAMES")); err == nil && v > 1 {
		n = v
	}
	if v, err := strconv.Atoi(os.Getenv("HW_MAXSHUT")); err == nil && v > 0 {
		defer camera.CapExposureForTest(v)()
	}
	tunings := []vpu.NRParams{vpu.NRStrongest, {K: 600, Strength: 150, BlendW: 16, BlendTh: 10},
		{K: 1000, Strength: 600, BlendW: 16, BlendTh: 30}, {K: 1000, Strength: 600, BlendW: 8, BlendTh: 60}}
	if s := os.Getenv("HW_NR"); s != "" {
		tunings = nil
		for _, f := range strings.Split(s, ";") {
			var p vpu.NRParams
			if _, err := fmt.Sscanf(f, "%d,%d,%d,%d", &p.K, &p.Strength, &p.BlendW, &p.BlendTh); err != nil {
				t.Fatal(err)
			}
			tunings = append(tunings, p)
		}
	}
	const W, H = 1456, 1088
	release, err := camera.Get().Acquire()
	if err != nil {
		t.Fatal(err)
	}
	frames := make(chan *camera.Frame, 4)
	stop := camera.Get().Frames.Listen(func(f *camera.Frame) {
		select {
		case frames <- f:
		default:
		}
	})
	time.Sleep(4 * time.Second) // the exposure settles
	for len(frames) > 0 {
		<-frames
	}
	var raws []*camera.Frame
	for len(raws) < n {
		raws = append(raws, <-frames)
	}
	stop()
	release()
	s, err := New(2112, 1568, Config{W: W, H: H, FPS: 30, Bitrate: 8000000, GOP: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pics := make([][]byte, n)
	for i, f := range raws {
		raw, _, _, bpl, tn, ok := f.ISPRaw()
		if !ok {
			t.Fatal("an empty frame")
		}
		if i == 0 {
			t.Logf("gain %.2fx, tone %+v", float64(f.Gain)/64, tn)
		}
		if _, _, err := s.Encode(raw, bpl, Tone(tn)); err != nil {
			t.Fatal(err)
		}
		b := s.ispOuts[s.pending]
		st := s.ispFmt.Planes[1].BytesPerLine
		pic := make([]byte, W*H*3/2)
		for y := 0; y < H; y++ {
			copy(pic[y*W:(y+1)*W], b.Planes[0][y*s.ispFmt.Planes[0].BytesPerLine:])
		}
		for y := 0; y < H/2; y++ {
			copy(pic[W*H+y*W:W*H+(y+1)*W], b.Planes[1][y*st:])
		}
		pics[i] = pic
	}
	ref := make([]float64, W*H*3/2)
	for _, p := range pics {
		for i, v := range p {
			ref[i] += float64(v)
		}
	}
	for i := range ref {
		ref[i] /= float64(n)
	}
	// the blocks of each plane, flattest first (by the reference's own spread)
	type block struct{ x, y int }
	blocks := func(off, w, h, step int) []block {
		var bs []block
		var sp []float64
		for by := 0; by+16 <= h; by += 16 {
			for bx := 0; bx+16*step <= w; bx += 16 * step {
				m, m2 := 0.0, 0.0
				for y := by; y < by+16; y++ {
					for x := bx; x < bx+16*step; x += step {
						v := ref[off+y*w+x]
						m, m2 = m+v, m2+v*v
					}
				}
				bs = append(bs, block{bx, by})
				sp = append(sp, m2/256-(m/256)*(m/256))
			}
		}
		idx := make([]int, len(bs))
		for i := range idx {
			idx[i] = i
		}
		sort.Slice(idx, func(a, b int) bool { return sp[idx[a]] < sp[idx[b]] })
		out := make([]block, len(bs))
		for i, k := range idx {
			out[i] = bs[k]
		}
		return out
	}
	type plane struct {
		name         string
		off, w, h, x int // x: the step between its samples in a row
	}
	planes := []plane{{"Y", 0, W, H, 1}, {"U", W * H, W, H / 2, 2}, {"V", W*H + 1, W, H / 2, 2}}
	pblocks := make([][]block, len(planes))
	for i, p := range planes {
		pblocks[i] = blocks(p.off, p.w, p.h, p.x)
	}
	measure := func(pic []byte) string {
		var out []string
		for i, p := range planes {
			rms := func(bs []block) float64 {
				s, c := 0.0, 0
				for _, b := range bs {
					for y := b.y; y < b.y+16; y++ {
						for x := b.x; x < b.x+16*p.x; x += p.x {
							k := p.off + y*p.w + x
							e := float64(pic[k]) - ref[k]
							s, c = s+e*e, c+1
						}
					}
				}
				return math.Sqrt(s / float64(c))
			}
			bs := pblocks[i]
			all := rms(bs)
			out = append(out, fmt.Sprintf("%s %.2f dB (rms %.2f, flat %.2f, busy %.2f)", p.name,
				20*math.Log10(255/all), all, rms(bs[:len(bs)/8]), rms(bs[len(bs)*7/8:])))
		}
		return strings.Join(out, "; ")
	}
	t.Logf("%d frames; the reference's own noise is about 1/%.1f of a frame's", n, math.Sqrt(float64(n)))
	for i := 0; i < min(3, n); i++ {
		t.Logf("frame %d as it came: %s", i, measure(pics[i]))
	}
	d, err := vpu.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	c, err := d.NewChroma(W, H, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("HW_DUMP")
	crop := func(name string, pic []byte) {
		if dir == "" {
			return
		}
		// a 384x288 crop of the middle, at 2x, as RGB
		const cw, ch, x0, y0 = 384, 288, (W - 384) / 2, (H - 288) / 2
		im := image.NewRGBA(image.Rect(0, 0, 2*cw, 2*ch))
		for y := 0; y < ch; y++ {
			for x := 0; x < cw; x++ {
				X, Y := x0+x, y0+y
				yy := float64(pic[Y*W+X])
				u := float64(pic[W*H+(Y/2)*W+(X&^1)]) - 128
				v := float64(pic[W*H+(Y/2)*W+(X&^1)+1]) - 128
				cl := func(f float64) uint8 { return uint8(min(max(f+0.5, 0), 255)) }
				px := color.RGBA{cl(yy + 1.402*v), cl(yy - 0.344*u - 0.714*v), cl(yy + 1.772*u), 255}
				for dy := 0; dy < 2; dy++ {
					for dx := 0; dx < 2; dx++ {
						im.SetRGBA(2*x+dx, 2*y+dy, px)
					}
				}
			}
		}
		f, err := os.Create(dir + "/" + name + ".png")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		pngEncode(f, im)
	}
	refPic := make([]byte, len(ref))
	for i, v := range ref {
		refPic[i] = byte(v + 0.5)
	}
	crop("reference", refPic)
	crop("frame0", pics[0])
	for k, p := range tunings {
		var took time.Duration
		var res string
		for i := 0; i < min(3, n); i++ {
			pic := append([]byte(nil), pics[i]...)
			began := time.Now()
			if err := c.NV12(pic[W*H:], W, p, 1000); err != nil {
				t.Fatal(err)
			}
			took += time.Since(began)
			if i == 0 {
				res = measure(pic)
				crop(fmt.Sprintf("nr%d", k), pic)
			}
		}
		t.Logf("%+v (%s a frame): %s", p, (took / time.Duration(min(3, n))).Round(100*time.Microsecond), res)
	}
	// The cost where the stream pays it: on the ISP's own plane (mapped uncached).
	{
		p := tunings[len(tunings)-1]
		uv, st := s.ispOuts[s.pending].Planes[1], s.ispFmt.Planes[1].BytesPerLine
		var whole, run time.Duration
		for i := 0; i < 5; i++ {
			began := time.Now()
			if err := c.NV12(uv, st, p, 1000); err != nil {
				t.Fatal(err)
			}
			whole += time.Since(began)
			began = time.Now()
			if err := c.Planar(p, 1000); err != nil {
				t.Fatal(err)
			}
			run += time.Since(began)
		}
		t.Logf("on the ISP's plane: %s a picture, %s of it the VPU's", whole/5, run/5)
	}
	// HW_LUMA ("K,S,BW,BT"): the same filter on the luma too, an experiment: Y in four strips of 272
	// rows, each as a plane of swnr (given as both its U and V, so the blend sees twice the change).
	if l := os.Getenv("HW_LUMA"); l != "" {
		var lp vpu.NRParams
		fmt.Sscanf(l, "%d,%d,%d,%d", &lp.K, &lp.Strength, &lp.BlendW, &lp.BlendTh)
		lc, err := d.NewChroma(2*W, 2*H/4, nil)
		if err != nil {
			t.Fatal(err)
		}
		pic := append([]byte(nil), pics[0]...)
		began := time.Now()
		if err := c.NV12(pic[W*H:], W, tunings[len(tunings)-1], 1000); err != nil {
			t.Fatal(err)
		}
		u, v := lc.Planes()
		for k := 0; k < 4; k++ {
			strip := pic[k*W*H/4 : (k+1)*W*H/4]
			copy(u, strip)
			copy(v, strip)
			if err := lc.Planar(lp, 1000); err != nil {
				t.Fatal(err)
			}
			copy(strip, u)
		}
		t.Logf("luma %+v too (%s): %s", lp, time.Since(began).Round(100*time.Microsecond), measure(pic))
		crop("luma", pic)
	}
}
