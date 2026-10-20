//go:build linux

package vpu

// swnr: the unit's own noise reduction on the VPU (cam_vpu2's vpu_lib_d2d_swnr), driven as Fire OS's
// camera HAL drives it from libcamalgo.vpunr.so (research/vpu-vision-p6.md §18). It is MediaTek's
// "fast SWNR" for the chroma of a 4:2:0 picture: per plane, an edge-stopping recursive filter run
// across, back, down and up the plane (its four IIR passes, the weights from the differences between
// neighbors through a table the host computes), then a blend back toward the plane as it came where
// the filter changed it a lot (so color edges stay). The luma is not touched: Fire OS's library left
// it to the ISP, and adds film grain to it on the cores (HFG), which is not ported.
//
// One picture is three jobs: the U plane (stages 1-4), the V plane, then the blend of both. Each plane
// is a separate W/2 x H/2 byte array, which must start on 4 KiB (as Fire OS's ION buffers did: off
// it, the DSP mangles bands of rows); the work buffer holds the planes as they came, the tables, and
// each plane's horizontal and vertical weights.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
)

// SWNR is the algorithm's name in cam_vpu2.
const SWNR = "vpu_lib_d2d_swnr"

// NRParams are the filter's tuning, as Fire OS's NVRAM gives them per ISO (ANR_FASTSWNR_*).
type NRParams struct {
	// K sets the most a pixel takes from its neighbor (256 × 2^(-7.84/K) of 256): the filter's reach.
	// Fire OS's strongest tuning ("worse", for the dimmest scenes) has 300.
	K int
	// Strength is the edge-stopping scale, in tenths of a level: neighbors that differ by d weigh
	// e^(-√2·d/(Strength/10)). 30 in the strongest tuning; more smooths larger differences.
	Strength int
	// BlendW is how much of the plane as it came is kept where the filter changed it little, in 64ths
	// (64: none of the filter's output is used); BlendTh the change, in levels of U plus V, from which
	// more of it is kept, all of it from twice that. 32 and 5 in the strongest tuning.
	BlendW, BlendTh int
}

// NRStrongest is Fire OS's own tuning for the dimmest scenes (libcamalgo.vpunr.so's VPU_IIR_WORSE),
// without its dither.
var NRStrongest = NRParams{K: 300, Strength: 30, BlendW: 32, BlendTh: 5}

// The host's tables (VPUIIRCore::IIRMain).
const (
	weightLen = 256 // by the difference between neighbors, 0-255
	blendLen  = 512 // by the change in U plus V, 0-511
)

// weightTable is the filter's weight for each difference between neighbors, as the host computes it:
// 256·e^(-√2·i/σ) rounded, times 2^(-7.84/K) in 16.16, saturating at 255.
func weightTable(p NRParams) (t [weightLen]byte) {
	gain := 65536
	if p.K >= 1 {
		gain = int(math.Exp2(-7.843848638152472/float64(p.K))*65536 + 0.5)
	}
	base := math.Exp(-math.Sqrt2 / (float64(max(p.Strength, 1)) / 10))
	for i := range t {
		m := int(math.Pow(base, float64(i))*256+0.5) * gain
		switch {
		case m >= 1<<24:
			t[i] = 255
		case m>>16 > 0:
			t[i] = byte(m >> 16)
		}
	}
	return t
}

// blendTable is the share of the plane as it came added to BlendW for each change in U plus V: none
// below BlendTh, rising to 64-BlendW at twice it.
func blendTable(p NRParams) (t [blendLen]byte) {
	if p.BlendW == 64 {
		return t
	}
	keep := 64 - p.BlendW
	slope := (keep << 1) & 0x1fe
	for i := range t {
		d := max(i, 1)
		switch {
		case d < p.BlendTh:
		case d >= 2*p.BlendTh:
			t[i] = byte(keep)
		default:
			t[i] = byte(slope * max(d-p.BlendTh, 0) / d)
		}
	}
	return t
}

// The algorithm's settings (its vpu_st_sett_swnr, 60 bytes): S32_Width, _Height, _Stride, _Stage,
// _NeedBlend, _NeedDither, _NRSmooth, then the work buffer's offsets: _weight_tbl, _iir_w_horz, _src,
// _iir_w_vert, _sd_tbl, _src_u, _src_v, _blend_tbl.
const settingsLen = 60

// The stages a job runs (S32_Stage): the host's "stage 0-3" (the four passes over one plane) and its
// "stage 4" (the blend of both planes).
const (
	stagePlane = 5
	stageBlend = 4
)

// Chroma is swnr readied for planes of one size, in the top of the data area: the U plane, the V plane,
// then the work buffer.
type Chroma struct {
	d      *Device
	w, h   int // a plane's size: the picture's half
	base   uint32
	params NRParams
	tabled bool
	buf    []byte // the cores' copies of a picture's chroma (NV12)
	// planes is the view of the data area the planes are used through: cached where the kernel has
	// one (cached), else the uncached one
	planes []byte
	cached bool
}

// layout: the U and V planes, then the work buffer's parts as the host lays them out (VPUIIRCore::Init).
func (c *Chroma) planeLen() uint32 { return uint32(c.w * c.h) }
func (c *Chroma) uOff() uint32     { return c.base }
func (c *Chroma) vOff() uint32     { return c.base + align(c.planeLen(), 4096) }
func (c *Chroma) workOff() uint32  { return c.vOff() + align(c.planeLen(), 4096) }

// workLen is the work buffer: the planes as they came, the blend and weight tables, each plane's
// horizontal and vertical weights, then rows the DSP keeps between tiles and the dither seeds.
func (c *Chroma) workLen() uint32 { return uint32(c.w*(6*c.h+8)) + 0x300 }

func (c *Chroma) size() uint32 { return c.workOff() + c.workLen() - c.base }

// NewChroma readies swnr for pictures of w x h (their chroma planes are w/2 x h/2) on d, whose data
// area it takes from the top down (about 3.2 MB at 1456x1088), and registers it with d. The algorithm
// comes from the unit's cam_vpu2 (or elf, if given).
func (d *Device) NewChroma(w, h int, elf []byte) (*Chroma, error) {
	if w <= 0 || h <= 0 || w%2 != 0 || h%2 != 0 || w > 8192 || h > 8192 {
		return nil, fmt.Errorf("vpu: swnr: a %dx%d picture", w, h)
	}
	if elf == nil {
		var err error
		if elf, err = ReadAlgorithm("cam_vpu2", SWNR); err != nil {
			return nil, err
		}
	}
	c := &Chroma{d: d, w: w / 2, h: h / 2}
	n := c.size()
	if int(n) > len(d.Data())-ReservedData {
		return nil, fmt.Errorf("vpu: swnr: a %dx%d picture needs %d bytes of the data area", w, h, n)
	}
	c.base = (uint32(len(d.Data())) - n) &^ 0xffff
	if err := d.reserve(c.base, true); err != nil {
		return nil, err
	}
	c.planes, c.cached = d.Cached(), d.Cached() != nil
	if !c.cached {
		c.planes = d.Data()
	}
	d.Register(SWNR, loaded(elf))
	return c, nil
}

func (c *Chroma) settings(stage int, plane int) []byte {
	le := binary.LittleEndian
	pl, w := c.planeLen(), uint32(c.w)
	s := make([]byte, settingsLen)
	blend := 0
	if c.params.BlendW != 64 {
		blend = 1
	}
	v := []uint32{
		w, uint32(c.h), w, uint32(stage), uint32(blend), 0, 0, // no dither: its seeds are Fire OS's table
		2*pl + 0x200,                    // the weight table
		2*pl + 0x300 + uint32(plane)*pl, // the plane's horizontal weights
		uint32(plane) * pl,              // the plane as it came
		4*pl + 0x300 + uint32(plane)*pl, // its vertical weights
		6*pl + 0x300 + 4*w,              // the dither seeds
		0, pl,                           // U and V as they came, for the blend
		2 * pl, // the blend table
	}
	for i, x := range v {
		le.PutUint32(s[4*i:], x)
	}
	return s
}

// Planar denoises the planes already in place (Planes) with p: three jobs, about 13 ms at 728x544.
// The tables are written again only when p changes.
func (c *Chroma) Planar(p NRParams, timeoutMs int) error {
	data := c.d.Data()
	if !c.tabled || p != c.params {
		if p.K < 1 || p.Strength < 1 || p.BlendW < 0 || p.BlendW > 64 || p.BlendTh < 0 {
			return fmt.Errorf("vpu: swnr: tuning %+v", p)
		}
		c.params = p
		pl := c.planeLen()
		wt, bt := weightTable(p), blendTable(p)
		work := data[c.workOff():]
		copy(work[2*pl:], bt[:])
		copy(work[2*pl+0x200:], wt[:])
		clear(work[6*pl+0x300+4*uint32(c.w) : c.workLen()]) // the seeds: unused without dither
		c.tabled = true
	}
	pl, wl := c.planeLen(), c.workLen()
	plane := func(port uint8, off uint32) Buf {
		return Buf{Port: port, Width: uint32(c.w), Height: uint32(c.h), Stride: uint32(c.w), Length: pl, Offset: off}
	}
	work := Buf{Port: 0, Width: wl, Height: 1, Stride: wl, Length: wl, Offset: c.workOff()}
	if err := c.d.Lock(SWNR); err != nil {
		return err
	}
	defer c.d.Unlock()
	// the planes (cached) to memory for the jobs, and after them the cores' stale copies dropped
	if err := c.sync(ForDevice); err != nil {
		return err
	}
	for i, j := range []struct {
		bufs     []Buf
		settings []byte
	}{
		{[]Buf{plane(1, c.uOff()), work}, c.settings(stagePlane, 0)},
		{[]Buf{plane(1, c.vOff()), work}, c.settings(stagePlane, 1)},
		{[]Buf{plane(1, c.uOff()), plane(2, c.vOff()), work}, c.settings(stageBlend, 0)},
	} {
		stage, err := c.d.Run(j.bufs, j.settings, timeoutMs)
		if err != nil {
			return fmt.Errorf("vpu: swnr job %d: %w", i, err)
		}
		if stage != 0x1400 {
			return fmt.Errorf("vpu: swnr job %d ended at stage %#x", i, stage)
		}
	}
	return c.sync(ForCPU)
}

// sync does the cache maintenance on both planes (they are next to each other, apart from the
// padding to 4 KiB).
func (c *Chroma) sync(flags uint32) error {
	return c.d.Sync(c.uOff(), c.vOff()+c.planeLen()-c.uOff(), flags)
}

// Planes are the U and V planes in the data area, w/2 x h/2 each: written before Planar, its
// output after. Cached for the cores where the kernel can (Planar keeps them in step with memory).
func (c *Chroma) Planes() (u, v []byte) {
	pl := c.planeLen()
	return c.planes[c.uOff() : c.uOff()+pl], c.planes[c.vOff() : c.vOff()+pl]
}

// NV12 denoises the chroma of an NV12 picture in place: uv is its interleaved plane (h/2 rows of
// stride bytes, U first). The plane is uncached for the cores, which read it slowly (80-400 MB/s, by
// core and clock): it is read once, in long runs, into the cores' own memory and split there, in
// nrBands bands side by side, which roughly halves the time. The planes in the data area are split
// into and joined from directly where they are cached; on a kernel without that view they are
// uncached too, and go through the cores' memory the same way.
func (c *Chroma) NV12(uv []byte, stride int, p NRParams, timeoutMs int) error {
	n := stride*(c.h-1) + 2*c.w
	if stride < 2*c.w || len(uv) < n {
		return errors.New("vpu: swnr: a chroma plane of the wrong size")
	}
	pl := c.w * c.h
	direct := c.cached
	need := n
	if !direct {
		need += 2 * pl
	}
	if len(c.buf) < need {
		c.buf = make([]byte, need)
	}
	in, du, dv := c.buf[:n], []byte(nil), []byte(nil)
	u, v := c.Planes()
	if !direct {
		du, dv = u, v
		u, v = c.buf[n:n+pl], c.buf[n+pl:n+2*pl]
	}
	c.bands(func(y0, y1 int) {
		a, b := y0*stride, min(y1*stride, n)
		copy(in[a:b], uv[a:b])
		Deinterleave(u[y0*c.w:], v[y0*c.w:], in[a:], c.w, y1-y0, stride)
		if !direct {
			copy(du[y0*c.w:y1*c.w], u[y0*c.w:y1*c.w])
			copy(dv[y0*c.w:y1*c.w], v[y0*c.w:y1*c.w])
		}
	})
	if err := c.Planar(p, timeoutMs); err != nil {
		return err
	}
	c.bands(func(y0, y1 int) {
		a, b := y0*stride, min(y1*stride, n)
		if !direct {
			copy(u[y0*c.w:y1*c.w], du[y0*c.w:y1*c.w])
			copy(v[y0*c.w:y1*c.w], dv[y0*c.w:y1*c.w])
		}
		Interleave(in[a:], u[y0*c.w:], v[y0*c.w:], c.w, y1-y0, stride)
		copy(uv[a:b], in[a:b])
	})
	return nil
}

// nrBands is how many bands of rows NV12 moves side by side.
const nrBands = 4

// bands calls f on nrBands bands of the plane's rows at once, and returns when all are done.
func (c *Chroma) bands(f func(y0, y1 int)) {
	var wg sync.WaitGroup
	for k := 0; k < nrBands; k++ {
		wg.Add(1)
		go func(y0, y1 int) {
			defer wg.Done()
			f(y0, y1)
		}(c.h*k/nrBands, c.h*(k+1)/nrBands)
	}
	wg.Wait()
}

// Deinterleave splits an interleaved plane (U V U V ..., stride bytes a row) into w x h planes.
func Deinterleave(u, v, uv []byte, w, h, stride int) {
	le := binary.LittleEndian
	for y := 0; y < h; y++ {
		row, ur, vr := uv[y*stride:y*stride+2*w], u[y*w:(y+1)*w], v[y*w:(y+1)*w]
		x := 0
		for ; x+8 <= w; x += 8 { // 16 bytes in, 8 of each out
			a, b := le.Uint64(row[2*x:]), le.Uint64(row[2*x+8:])
			le.PutUint64(ur[x:], evens(a)|evens(b)<<32)
			le.PutUint64(vr[x:], evens(a>>8)|evens(b>>8)<<32)
		}
		for ; x < w; x++ {
			ur[x], vr[x] = row[2*x], row[2*x+1]
		}
	}
}

// evens packs the even bytes of a word into its low half.
func evens(a uint64) uint64 {
	a &= 0x00ff00ff00ff00ff
	a = (a | a>>8) & 0x0000ffff0000ffff
	return (a | a>>16) & 0xffffffff
}

// spread is evens' inverse: the low half's bytes into the even bytes.
func spread(a uint64) uint64 {
	a &= 0xffffffff
	a = (a | a<<16) & 0x0000ffff0000ffff
	return (a | a<<8) & 0x00ff00ff00ff00ff
}

// Interleave is Deinterleave's inverse; the bytes past 2w in each row are left alone.
func Interleave(uv, u, v []byte, w, h, stride int) {
	le := binary.LittleEndian
	for y := 0; y < h; y++ {
		row, ur, vr := uv[y*stride:y*stride+2*w], u[y*w:(y+1)*w], v[y*w:(y+1)*w]
		x := 0
		for ; x+8 <= w; x += 8 {
			a, b := le.Uint64(ur[x:]), le.Uint64(vr[x:])
			le.PutUint64(row[2*x:], spread(a)|spread(b)<<8)
			le.PutUint64(row[2*x+8:], spread(a>>32)|spread(b>>32)<<8)
		}
		for ; x < w; x++ {
			row[2*x], row[2*x+1] = ur[x], vr[x]
		}
	}
}
