//go:build linux

package vpu

// apunn: the unit's NeuroPilot runtime (cam_vpu2's apu_lib_apunn) driven without Fire OS's NNAPI HAL
// (research/vpu-vision-p6.md §16). The HAL is Cadence's XRP host carried over one D2D job: the job's
// only buffer, on port 1, is a 104-byte XRP command (struct xrp_dsp_cmd, Cadence's layout plus two
// words of MediaTek's); the command's input is a list of 152-byte operation records, its buffers the
// tensors, its output a 4-byte status. A record is {XFL op, the op's parameters (128 bytes), the
// indices of its input and output tensors}; the parameters are what the HAL's kernelSetup computes on
// the ARM (tiling against the DSP's local memory, requantization, reordered coefficients), ported
// below for CONV_2D and DEPTHWISE_CONV_2D.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// APUNN is the runtime's name in cam_vpu2.
const APUNN = "apu_lib_apunn"

// The runtime's operations (its XFL_* names, in its order)
const (
	OpConv2D          = 0
	OpDepthwiseConv2D = 1
	opLocalMemInfo    = 27
	opVersion         = 28
)

// DataPA is where the data area (Device.Data) starts for the core.
const DataPA = 0x7eb00000

// The data area's first 64 KiB carry the command: tensors go from ReservedData on.
const (
	cmdOff       = 0x0    // the XRP command
	outDataOff   = 0x80   // its output, when longer than 16 bytes
	dspBufOff    = 0x100  // {flags, size, addr} per tensor
	inDataOff    = 0x1000 // the records
	ReservedData = 0x10000

	recordSize = 152
	paramsMax  = 128
	cmdSize    = 104
)

// The namespace apunn registers with XRP (the HAL's XtensaDriver opens its queue with it)
var apunnNSID = [16]byte{0xde, 0x63, 0xdb, 0xbe, 0x4a, 0x99, 0x48, 0x89, 0x90, 0x83, 0xf0, 0x7b, 0xf8, 0x61, 0x09, 0x7a}

// Tensor is one of a command's buffers, in the data area.
type Tensor struct {
	Offset, Size uint32
	Write        bool // the DSP writes it
}

// Op is one operation record.
type Op struct {
	Type    uint32
	Params  []byte
	In, Out []int // indices into the command's tensors (6 and 2 at most)
}

// cmdBufs is how many vpu_buffers the job carries, all the same command (the HAL adds it 5 times).
var cmdBufs = 1

// Command runs ops as one XRP command and returns its output (outLen bytes; the runtime's status
// first). On the shared device, apunn is loaded again first if another algorithm ran since.
func (d *Device) Command(ops []Op, tensors []Tensor, outLen int, timeoutMs int) ([]byte, error) {
	if err := d.Lock(APUNN); err != nil {
		return nil, err
	}
	defer d.Unlock()
	le := binary.LittleEndian
	data := d.Data()
	if len(ops) == 0 || inDataOff+len(ops)*recordSize > ReservedData || dspBufOff+12*len(tensors) > inDataOff ||
		outLen > dspBufOff-outDataOff {
		return nil, errors.New("vpu: a command of the wrong shape")
	}
	clear(data[:ReservedData])
	for i, op := range ops {
		r := data[inDataOff+i*recordSize : inDataOff+(i+1)*recordSize]
		if len(op.Params) > paramsMax || len(op.In) > 6 || len(op.Out) > 2 {
			return nil, fmt.Errorf("vpu: op %d of the wrong shape", i)
		}
		le.PutUint32(r, op.Type)
		copy(r[4:], op.Params)
		for k := 0; k < 8; k++ {
			v := 0xffff
			if k < 6 && k < len(op.In) {
				v = op.In[k]
			} else if k >= 6 && k-6 < len(op.Out) {
				v = op.Out[k-6]
			}
			le.PutUint16(r[132+2*k:], uint16(v))
		}
	}
	for i, t := range tensors {
		if t.Offset < ReservedData || int(t.Offset)+int(t.Size) > len(data) {
			return nil, fmt.Errorf("vpu: tensor %d is not in the data area", i)
		}
		flags := uint32(1) // XRP_READ
		if t.Write {
			flags = 3
		}
		b := data[dspBufOff+12*i:]
		le.PutUint32(b, flags)
		le.PutUint32(b[4:], t.Size)
		le.PutUint32(b[8:], DataPA+t.Offset)
	}
	c := data[cmdOff : cmdOff+cmdSize]
	le.PutUint32(c[0:], 1|4) // REQUEST_VALID | REQUEST_NSID
	le.PutUint32(c[4:], uint32(len(ops)*recordSize))
	le.PutUint32(c[8:], uint32(outLen))
	le.PutUint32(c[12:], uint32(12*len(tensors)))
	le.PutUint32(c[16:], DataPA+inDataOff)
	if outLen > 16 {
		le.PutUint32(c[32:], DataPA+outDataOff)
	}
	if len(tensors) == 1 {
		copy(c[48:60], data[dspBufOff:dspBufOff+12])
	} else if len(tensors) > 1 {
		le.PutUint32(c[48:], DataPA+dspBufOff)
	}
	copy(c[64:80], apunnNSID[:])
	le.PutUint32(c[80:], 1)
	bufs := make([]Buf, cmdBufs)
	for i := range bufs {
		bufs[i] = Buf{Port: 1, Width: cmdSize, Height: 1, Stride: cmdSize, Length: cmdSize, Offset: cmdOff}
	}
	if _, err := d.Run(bufs, nil, timeoutMs); err != nil {
		return nil, err
	}
	if f := le.Uint32(c); f&0xa != 2 {
		return nil, fmt.Errorf("vpu: apunn did not answer (flags %#x)", f)
	}
	out := make([]byte, outLen)
	if outLen > 16 {
		copy(out, data[outDataOff:])
	} else {
		copy(out, c[32:])
	}
	return out, nil
}

// OpenAPUNN takes a hold on the shared VPU (Acquire: booted from the unit's cam_vpu1 if it was not),
// registers apunn (cam_vpu2) and checks the runtime is the one this package speaks to: about 10 ms
// when the core is already up, most of it reading the partitions. The core stays powered until the
// device is closed by every holder.
func OpenAPUNN() (*Device, LocalMem, error) {
	elf, err := ReadAlgorithm("cam_vpu2", APUNN)
	if err != nil {
		return nil, LocalMem{}, err
	}
	d, err := Acquire()
	if err != nil {
		return nil, LocalMem{}, err
	}
	d.Register(APUNN, loaded(elf))
	lm, err := d.checkAPUNN()
	if err != nil {
		d.Close()
		return nil, LocalMem{}, err
	}
	return d, lm, nil
}

func (d *Device) checkAPUNN() (LocalMem, error) {
	v, err := d.Version()
	if err != nil {
		return LocalMem{}, err
	}
	if v != "XTENSA_ANN_V1" {
		return LocalMem{}, fmt.Errorf("vpu: apunn %q, not the XTENSA_ANN_V1 this speaks", v)
	}
	return d.LocalMem()
}

// Version asks the runtime for its version tag (XTENSA_ANN_V1).
func (d *Device) Version() (string, error) {
	out, err := d.Command([]Op{{Type: opVersion}}, nil, 20, 1000)
	if err != nil {
		return "", err
	}
	if s := binary.LittleEndian.Uint32(out); s != 0 {
		return "", fmt.Errorf("vpu: version: status %d", s)
	}
	n := 4
	for n < len(out) && out[n] != 0 {
		n++
	}
	return string(out[4:n]), nil
}

// LocalMem is the DSP's local memory as the runtime reports it.
type LocalMem struct {
	Banks, Contiguous uint32
	Size              [2]uint32
}

// LocalMem asks the runtime for its local memory.
func (d *Device) LocalMem() (LocalMem, error) {
	le := binary.LittleEndian
	out, err := d.Command([]Op{{Type: opLocalMemInfo}}, nil, 20, 1000)
	if err != nil {
		return LocalMem{}, err
	}
	if s := le.Uint32(out); s != 0 {
		return LocalMem{}, fmt.Errorf("vpu: local memory: status %d", s)
	}
	return LocalMem{le.Uint32(out[4:]), le.Uint32(out[8:]), [2]uint32{le.Uint32(out[12:]), le.Uint32(out[16:])}}, nil
}

// lmInfo is the HAL's localMem_info_t: what setupDeviceMemoryLayout places buffers into.
type lmInfo struct{ mode, banks, size0, size1 uint32 }

func (l LocalMem) info() lmInfo {
	m := lmInfo{banks: l.Banks}
	if l.Banks >= 2 {
		m.mode = 2
		if l.Contiguous != 0 {
			m.mode = 1
		}
	}
	if l.Banks > 0 {
		m.size0 = l.Size[0]
	}
	if l.Banks > 1 {
		m.size1 = l.Size[1]
	}
	return m
}

// setupDeviceMemoryLayout: with two banks, the buffers, largest first, go in bank 0 while they fit,
// the rest in bank 1 (mask: which); otherwise one after the other, the mask marking those that start
// past bank 0. The DSP lays the buffers out again from the same sizes and this result.
func setupDeviceMemoryLayout(sizes []uint32, m lmInfo) (out [4]uint32, ok bool) {
	if m.banks == 2 {
		s := append([]uint32(nil), sizes...)
		idx := make([]int, len(s))
		for i := range idx {
			idx[i] = i
		}
		for i := 1; i < len(s); i++ {
			key, ki := s[i], idx[i]
			j := i
			for j > 0 && key > s[j-1] {
				s[j], idx[j] = s[j-1], idx[j-1]
				j--
			}
			s[j], idx[j] = key, ki
		}
		var used0, used1, mask uint32
		fits := true
		for i, sz := range s {
			if sz == 0 {
				break
			}
			if used0+sz <= m.size0 {
				used0 += sz
				continue
			}
			used1 += sz
			if used1 > m.size1 {
				fits = false
				break
			}
			mask |= 1 << idx[i]
		}
		if fits {
			return [4]uint32{2, mask, used0, used1}, true
		}
	}
	if m.mode > 1 {
		return out, false
	}
	var acc, mask uint32
	total := m.size0 + m.size1
	for i, sz := range sizes {
		if sz == 0 {
			continue
		}
		if acc+sz >= total {
			return out, false
		}
		if acc >= m.size0 {
			mask |= 1 << i
		}
		acc += sz
	}
	return [4]uint32{m.mode, mask, acc, 0}, true
}

// convParams is the HAL's conv_params_t, 81 bytes in the record.
type convParams struct {
	banks                         [4]uint32 // +4: setupDeviceMemoryLayout's result
	cin, inW, inH                 uint32    // +20
	cout, outW, outH, n           uint32    // +32
	mult, shift                   int32     // +48: the output multiplier (Q31) and right shift
	tC, tW, tH                    uint32    // +56: the output tile
	kw, kh                        uint16    // +68
	stride, flags, offX, offY     uint8     // +72; flags: 0 activation, 1 loop order, 2-4 kind, 5 double coefficients
	zIn, zW, zOut, actMin, actMax uint8     // +76
}

func (p *convParams) kind() int { return int(p.flags>>2) & 7 }

func (p *convParams) setKind(k int) { p.flags = p.flags&^0x1c | uint8(k)<<2 }

func (p *convParams) bytes() []byte {
	le := binary.LittleEndian
	b := make([]byte, 81)
	le.PutUint32(b, 81)
	for i, v := range []uint32{p.banks[0], p.banks[1], p.banks[2], p.banks[3], p.cin, p.inW, p.inH, p.cout,
		p.outW, p.outH, p.n, uint32(p.mult), uint32(p.shift), p.tC, p.tW, p.tH} {
		le.PutUint32(b[4+4*i:], v)
	}
	le.PutUint16(b[68:], p.kw)
	le.PutUint16(b[70:], p.kh)
	copy(b[72:], []byte{p.stride, p.flags, p.offX, p.offY, p.zIn, p.zW, p.zOut, p.actMin, p.actMax})
	return b
}

// The kernels (conv_kernel_kind_e)
const (
	KindAuto      = 0
	KindMOD       = 1 // 1x1 and MxN, 64 output channels a block
	KindQM24      = 2 // 24-bit accumulators
	KindQM32      = 3 // 32-bit accumulators
	KindFC        = 4 // one output pixel a tile: the HAL's fallback
	kindGSMOW     = 5 // 3x3 stride 1 (not ported)
	KindDepthwise = 6
)

func align(v, a uint32) uint32   { return (v + a - 1) &^ (a - 1) }
func ceilDiv(a, b uint32) uint32 { return (a + b - 1) / b }

// memInfo is the HAL's conv_mem_info_t, the sizes AllocateLocalMemConv2D works out.
type memInfo struct{ nTC, nTW, nTH, inTile, coefTile, outTile, bias, scratch uint32 }

// allocConv is AllocateLocalMemConv2D.
func (p *convParams) allocConv(lm lmInfo) (memInfo, bool) {
	var m memInfo
	m.nTC, m.nTW, m.nTH = ceilDiv(p.cout, p.tC), ceilDiv(p.outW, p.tW), ceilDiv(p.outH, p.tH)
	s, kw, kh := uint32(p.stride), uint32(p.kw), uint32(p.kh)
	m.inTile = p.cin * ((p.tW-1)*s + kw) * ((p.tH-1)*s + kh)
	m.outTile = p.tC * p.tW * p.tH
	m.bias = 4 * p.cout
	coef0 := kw * kh * p.cin
	switch k := p.kind(); k {
	case KindQM24, KindQM32:
		if p.tW*p.tH < 4 {
			return m, false
		}
		m.outTile = p.tC * align(p.tW*p.tH, 4)
		row := align(align(kw*p.cin, 4)*kh, 8)
		m.coefTile = row * p.tC
		m.scratch = (4*p.tW*p.tH + 12) &^ 15
		if kw == 1 && kh == 1 && p.cin&7 == 0 {
			m.inTile += 2 * row
		} else {
			m.scratch += row * align(p.tW*p.tH, 4)
		}
	case KindMOD:
		if p.tW == 1 && p.tH == 1 {
			return m, false
		}
		m.coefTile = align(p.tC, 64) * coef0
		m.scratch = 4 * p.tW * p.tH
	case kindGSMOW:
		return m, false
	default:
		m.coefTile = p.tC * coef0
	}
	dblIn := !(m.nTW == 1 && m.nTH == 1 && p.n == 1)
	scr, coef, out, in, bias := align(m.scratch, 64), align(m.coefTile, 64), align(m.outTile, 64), align(m.inTile, 64), align(m.bias, 64)
	dblCoef := m.nTC >= 2 && p.flags&0x20 != 0
	dblOut := m.nTC > 1 || dblIn
	sizes := make([]uint32, 8)
	sizes[0], sizes[1], sizes[3], sizes[5] = bias, in, out, coef
	if dblIn {
		sizes[2] = in
	}
	if dblOut {
		sizes[4] = out
	}
	if dblCoef {
		sizes[6] = coef
	}
	if m.scratch != 0 {
		sizes[7] = scr
	}
	var total uint32
	for _, v := range sizes {
		total += v
	}
	if total == 0 {
		return m, false
	}
	var ok bool
	p.banks, ok = setupDeviceMemoryLayout(sizes, lm)
	return m, ok
}

// The HAL's cost models (predicted cycles a tile), for choosing among kernels and tilings
func modelQM32(p *convParams) float32 {
	r := align(uint32(p.kw)*p.cin, 4) * uint32(p.kh)
	r2 := align(r, 8)
	q := ((61 + r2/4) >> 1) / 31
	d := float64(28*q) + float64((r+7)>>3)*8.2 + 123.0
	d = float64(56+4*(3*uint32(p.kh)+(r2+63)>>6)) + d*float64((p.tC+63)>>6)
	return 125.0 + float32((p.tW*p.tH+3)>>2)*float32(d+115.0)
}

func modelQM24(p *convParams) float32 {
	r := align(align(uint32(p.kw)*p.cin, 4)*uint32(p.kh), 8)
	d := 61.0 + float64(1|r>>2)*8.2*0.5
	d = float64(56+4*(3*uint32(p.kh)+(r+63)>>6)) + d*float64((p.tC+63)>>6)
	return 125.0 + float32((p.tW*p.tH+3)>>2)*float32(d+115.0)
}

func model1x1(p *convParams) float32 {
	i := 0
	if p.cin&3 == 0 {
		i = 1
	}
	a, b, c, dd, e := [2]float64{33.32729354, 28.02605354}, [2]float64{118.17603777, 100.66974692},
		[2]float64{4.58488786, 4.56608672}, [2]float64{56.5491165, 53.6597693}, [2]float64{74.5587789, 82.13547351}
	x := b[i] + c[i]*float64(p.cin>>2) + 39.0
	x = a[i] + x*float64((p.tW+1)>>1)
	x = e[i] + x*float64((p.tH+1)>>1)
	x = dd[i] + x*float64((p.tC+63)>>6)
	return float32(p.tW*p.tH*(35+(126+2*p.cin)>>6)) + float32(x)
}

func modelMxN(p *convParams) float32 {
	ck := p.cin * uint32(p.kw)
	i := 0
	if ck&3 == 0 {
		i = 1
	}
	c0, c1, c2, c3, c4, c5 := [2]float64{2.1639086, 2.11535784}, [2]float64{1.32380411, 1.2839801},
		[2]float64{74.24374647, 62.05437559}, [2]float64{7.4981325, 7.04195927}, [2]float64{4.50286598, 4.26073724},
		[2]float64{67.50409869, 50.45233397}
	x := c5[i] + c4[i]*float64(ck>>2)
	x = c2[i] + x*float64(p.kh)
	x = c3[i] + x*float64((p.tW+1)>>1)
	x = c1[i] + x*float64((p.tH+1)>>1)
	x = c0[i] + x*float64((p.tC+63)>>6)
	return float32((uint32(p.kh)*(9+(ck+63)>>6)+15)*p.tW*p.tH) + float32(x)
}

// searchTileDims: the whole output as one tile if it fits; else the cheapest tiling by the model, with
// and without double-buffered coefficients, channel tiles in steps of step.
func (p *convParams) search(kind int, step uint32, model func(*convParams) float32, lm lmInfo) (float32, bool) {
	p.tC, p.tW, p.tH = p.cout, p.outW, p.outH
	p.setKind(kind)
	p.flags |= 0x20
	if _, ok := p.allocConv(lm); ok {
		return model(p), true
	}
	best := float32(9.999999848243207e+30)
	var bp convParams
	k := uint32(p.kw) * p.cin * uint32(p.kh)
	for dbl := 1; dbl >= 0; dbl-- {
		p.flags = p.flags&^0x20 | uint8(dbl)<<5
		for c := step; c <= p.cout+step-1; c += step {
			p.tC = min(p.cout, c)
			var cc float32
			if dbl != 0 {
				cc = float32(256 + (k*p.tC+15)/16)
			}
			nTC := ceilDiv(p.cout, p.tC)
			fixed := cc * float32(nTC)
			if p.outW+p.outH < 2 {
				continue
			}
			for h := int(p.outH); int(p.outW)+h+1 > 2; h-- {
				if h > 0 {
					p.tW, p.tH = p.outW, uint32(h)
				} else {
					p.tW, p.tH = uint32(int(p.outW)+h-1), 1
				}
				m, ok := p.allocConv(lm)
				if !ok {
					continue
				}
				t := fixed + (model(p)+1276.0)*float32(m.nTC*m.nTW*m.nTH)
				if t < best {
					best, bp = t, *p
				}
			}
		}
	}
	if best >= 9.999999848243207e+30 {
		return 0, false
	}
	*p = bp
	p.allocConv(lm)
	return best, true
}

// setupConv2D is SetupConv2D with the kernel left to it (KindAuto) or forced; GS_MOW is not ported,
// so a 3x3 stride-1 conv the HAL would give it gets the next best.
func (p *convParams) setupConv2D(allow24 bool, kind int, lm lmInfo) bool {
	if kind == KindAuto || kind == KindQM24 || kind == KindMOD {
		var best convParams
		var bc float32
		if allow24 && (kind == KindAuto || kind == KindQM24) {
			q := *p
			if c, ok := q.search(KindQM24, 1, modelQM24, lm); ok {
				best, bc = q, c
			}
		}
		if (kind == KindAuto || kind == KindMOD) && p.kw <= 16 && p.kh <= 16 {
			q := *p
			model := modelMxN
			if p.kw == 1 && p.kh == 1 {
				model = model1x1
			}
			if c, ok := q.search(KindMOD, 64, model, lm); ok && (bc == 0 || c < bc) {
				best, bc = q, c
			}
		}
		if bc > 0 {
			*p = best
			return true
		}
	}
	if kind == KindAuto || kind == KindQM32 {
		q := *p
		if _, ok := q.search(KindQM32, 1, modelQM32, lm); ok {
			*p = q
			return true
		}
	}
	if kind == KindAuto || kind == KindFC {
		p.tW, p.tH = 1, 1
		p.flags = p.flags&0xc3 | 0x30
		for c := p.cout; c >= 1; c-- {
			p.tC = c
			if _, ok := p.allocConv(lm); ok {
				return true
			}
		}
	}
	return false
}

// setupDepthwise is SetupDepthwise: channel tiles of 32, rows of the output as tall as fit.
func (p *convParams) setupDepthwise(lm lmInfo) bool {
	p.setKind(KindDepthwise)
	if p.kw > 16 || p.kh > 16 {
		return false
	}
	s := uint32(p.stride)
	for c := align(p.cout, 32); c > 0; c -= 32 {
		p.tC = min(p.cout, c)
		for h := int(p.outH); int(p.outW)+h+1 > 2; h-- {
			if h > 0 {
				p.tW, p.tH = p.outW, uint32(h)
			} else {
				p.tW, p.tH = uint32(int(p.outW)+h-1), 1
			}
			nTC, nTW, nTH := ceilDiv(p.cout, p.tC), ceilDiv(p.outW, p.tW), ceilDiv(p.outH, p.tH)
			in := align(p.tC*((p.tW-1)*s+uint32(p.kw))*((p.tH-1)*s+uint32(p.kh)), 64)
			out := align(p.tC*p.tW*p.tH, 64)
			bias := align(4*p.cout, 64)
			coef := align(p.tC, 64) * uint32(p.kw) * uint32(p.kh)
			dbl := true
			if nTC <= 1 && nTW == 1 && p.n == 1 {
				dbl = nTH != 1
			}
			sizes := make([]uint32, 7)
			sizes[0], sizes[1], sizes[3], sizes[5] = bias, in, out, coef
			if dbl {
				sizes[2], sizes[4] = in, out
			}
			if nTC >= 2 {
				sizes[6] = coef
			}
			if b, ok := setupDeviceMemoryLayout(sizes, lm); ok {
				p.banks = b
				return true
			}
		}
	}
	return false
}

// Conv is one quantized convolution as NNAPI 1.1 has it: uint8 tensors, one scale and zero point
// each (TENSOR_QUANT8_ASYMM), int32 bias at InScale*WScale, NHWC, explicit padding.
type Conv struct {
	Depthwise                 bool // DEPTHWISE_CONV_2D; OutC = InC × the multiplier
	InW, InH, InC, OutC       int
	KW, KH, Stride            int
	PadL, PadR, PadT, PadB    int
	InScale, WScale, OutScale float64
	InZero, WZero, OutZero    uint8
	Act                       int     // 0 none, 1 RELU, 2 RELU1, 3 RELU6
	Weights                   []uint8 // [OutC][KH][KW][InC]; depthwise [KH][KW][OutC]
	Bias                      []int32
	Kind                      int // KindAuto as the HAL would choose, or one forced
}

func (c *Conv) OutW() int { return (c.InW+c.PadL+c.PadR-c.KW)/c.Stride + 1 }
func (c *Conv) OutH() int { return (c.InH+c.PadT+c.PadB-c.KH)/c.Stride + 1 }

// QuantizeMultiplierSmallerThanOne, as NNAPI's
func quantizeMultiplier(real float64) (int32, int32, bool) {
	if real < 0 || real >= 1 {
		return 0, 0, false
	}
	if real == 0 {
		return 0, 0, true
	}
	q, e := math.Frexp(real)
	shift := -e
	qf := int64(math.Round(q * (1 << 31)))
	if qf == 1<<31 {
		qf /= 2
		shift--
	}
	return int32(qf), int32(shift), true
}

func (c *Conv) actRange() (uint8, uint8) {
	q := func(f float64) int { return int(c.OutZero) + int(math.Round(f/c.OutScale)) }
	lo, hi := 0, 255
	switch c.Act {
	case 1:
		lo = max(lo, q(0))
	case 2:
		lo, hi = max(lo, q(-1)), min(hi, q(1))
	case 3:
		lo, hi = max(lo, q(0)), min(hi, q(6))
	}
	return uint8(lo), uint8(hi)
}

// Prepared is a conv ready for the runtime: its record's parameters and its two constant tensors.
type Prepared struct {
	Type   uint32
	Params []byte
	Coef   []byte // the coefficients in the kernel's order
	Bias   []byte // int32, as the kernel wants it
	Kind   int
	Tile   [3]int // output channels, columns, rows a tile
}

// Prepare does what the HAL's flkConv_kernelSetup does for the conv.
func (c *Conv) Prepare(lmem LocalMem) (*Prepared, error) {
	lm := lmem.info()
	ow, oh := c.OutW(), c.OutH()
	if c.Depthwise && c.OutC%c.InC != 0 {
		return nil, errors.New("vpu: depthwise output channels not a multiple of the input's")
	}
	if c.Stride < 1 || c.Stride > 255 || ow < 1 || oh < 1 {
		return nil, errors.New("vpu: a conv of the wrong shape")
	}
	nw := c.OutC * c.KH * c.KW * c.InC
	if c.Depthwise {
		nw = c.KH * c.KW * c.OutC
	}
	if len(c.Weights) != nw || len(c.Bias) != c.OutC {
		return nil, errors.New("vpu: weights or bias of the wrong size")
	}
	prod := c.InScale * c.WScale
	if !(prod < c.OutScale) || c.OutScale < 0 || prod < 0 {
		return nil, errors.New("vpu: scales out of range")
	}
	mult, shift, ok := quantizeMultiplier(prod / c.OutScale)
	if !ok {
		return nil, errors.New("vpu: scales out of range")
	}
	p := convParams{
		cin: uint32(c.InC), inW: uint32(c.InW), inH: uint32(c.InH),
		cout: uint32(c.OutC), outW: uint32(ow), outH: uint32(oh), n: 1,
		mult: mult, shift: shift,
		kw: uint16(c.KW), kh: uint16(c.KH), stride: uint8(c.Stride),
		offX: uint8(c.KW>>1 - c.PadL), offY: uint8(c.KH>>1 - c.PadT),
		zIn: c.InZero, zW: c.WZero, zOut: c.OutZero,
	}
	p.actMin, p.actMax = c.actRange()
	if c.Act != 0 {
		p.flags |= 1
	}
	pr := &Prepared{Type: OpConv2D}
	if c.Depthwise {
		pr.Type = OpDepthwiseConv2D
		if !p.setupDepthwise(lm) {
			return nil, errors.New("vpu: the depthwise conv does not fit the local memory")
		}
	} else {
		allow24 := !mayOverflow24(c.Weights, c.KW*c.KH*c.InC, c.OutC)
		if !p.setupConv2D(allow24, c.Kind, lm) {
			return nil, errors.New("vpu: the conv does not fit the local memory")
		}
		m, _ := p.allocConv(lm)
		s := uint64(p.n * m.nTW * m.nTH)
		x := uint64(m.nTC-1) * s * uint64(m.inTile)
		y := (s - 1) * uint64(m.nTC) * uint64(m.coefTile)
		p.flags &^= 2
		if int64(y) >= int64(x) {
			p.flags |= 2
		}
	}
	pr.Kind, pr.Tile = p.kind(), [3]int{int(p.tC), int(p.tW), int(p.tH)}
	pr.Coef, pr.Bias = c.reorder(&p)
	pr.Params = p.bytes()
	return pr, nil
}

// mayOverflow24 is MayOverflowAccumulator24: 128 × Σ|w−128| of some output channel reaches 2^23.
func mayOverflow24(w []uint8, k, n int) bool {
	for o := 0; o < n; o++ {
		var s int64
		for _, v := range w[o*k : (o+1)*k] {
			d := int64(v) - 128
			if d < 0 {
				d = -d
			}
			s += 128 * d
		}
		if s >= 1<<23 {
			return true
		}
	}
	return false
}

// reorder is ConvReorderCoefficients: the coefficients as the kernel walks them (signed, w−128, for
// the conv kernels), and the bias with the zero points' terms folded in.
func (c *Conv) reorder(p *convParams) ([]byte, []byte) {
	le := binary.LittleEndian
	cin, cout, kw, kh, tc := c.InC, c.OutC, c.KW, c.KH, int(p.tC)
	k := kw * kh * cin
	bias := make([]byte, 4*cout)
	var coef []byte
	foldBias := func() {
		zi, zw := int32(c.InZero), int32(c.WZero)
		for o := 0; o < cout; o++ {
			var sum int32
			for _, v := range c.Weights[o*k : (o+1)*k] {
				sum += int32(v)
			}
			le.PutUint32(bias[4*o:], uint32(c.Bias[o]+(zw*zi-16384)*int32(k)+(128-zi)*sum))
		}
	}
	w := func(o, y, x, i int) byte { return c.Weights[((o*kh+y)*kw+x)*cin+i] + 128 }
	switch p.kind() {
	case KindQM24, KindQM32:
		row := int(align(uint32(kw*cin), 4))
		tail := int(align(uint32(row*kh), 8)) - row*kh
		for t := 0; t*tc < cout; t++ {
			n := min(cout-t*tc, tc)
			for c0 := 0; c0 < n; c0 += 64 {
				nb := min(n-c0, 64)
				for half := 0; half < 2; half++ {
					for y := 0; y < kh; y++ {
						for e := 0; e < row; e++ {
							if (e>>1)&1 != half {
								continue
							}
							if e < kw*cin {
								x, i := e/cin, e%cin
								for j := 0; j < nb; j++ {
									coef = append(coef, w(t*tc+c0+j, y, x, i))
								}
							} else {
								coef = append(coef, make([]byte, nb)...)
							}
						}
					}
					coef = append(coef, make([]byte, tail/2*nb)...)
				}
			}
		}
		foldBias()
	case KindMOD:
		for t := 0; t*tc < cout; t++ {
			n := min(cout-t*tc, tc)
			for y := 0; y < kh; y++ {
				for x := 0; x < kw; x++ {
					for i := 0; i < cin; i++ {
						for j := 0; j < n; j++ {
							coef = append(coef, w(t*tc+j, y, x, i))
						}
						coef = append(coef, make([]byte, int(align(uint32(n), 64))-n)...)
					}
				}
			}
		}
		foldBias()
	case KindDepthwise:
		for t := 0; t*tc < cout; t++ {
			n := min(cout-t*tc, tc)
			for y := 0; y < kh; y++ {
				for x := 0; x < kw; x++ {
					coef = append(coef, c.Weights[(y*kw+x)*cout+t*tc:(y*kw+x)*cout+t*tc+n]...)
					coef = append(coef, make([]byte, int(align(uint32(n), 64))-n)...)
				}
			}
		}
		for o, b := range c.Bias {
			le.PutUint32(bias[4*o:], uint32(b))
		}
	default: // the fallback takes them as they are
		coef = append(coef, c.Weights...)
		for o, b := range c.Bias {
			le.PutUint32(bias[4*o:], uint32(b))
		}
	}
	return coef, bias
}

// Reference is the conv on the CPU, as NNAPI's reference kernels compute it.
func (c *Conv) Reference(in []uint8) []uint8 {
	ow, oh := c.OutW(), c.OutH()
	mult, shift, _ := quantizeMultiplier(c.InScale * c.WScale / c.OutScale)
	lo, hi := c.actRange()
	out := make([]uint8, ow*oh*c.OutC)
	mul := c.OutC / c.InC
	for oy := 0; oy < oh; oy++ {
		for ox := 0; ox < ow; ox++ {
			for o := 0; o < c.OutC; o++ {
				acc := c.Bias[o]
				for ky := 0; ky < c.KH; ky++ {
					for kx := 0; kx < c.KW; kx++ {
						ix, iy := ox*c.Stride-c.PadL+kx, oy*c.Stride-c.PadT+ky
						if ix < 0 || iy < 0 || ix >= c.InW || iy >= c.InH {
							continue
						}
						if c.Depthwise {
							x := int32(in[(iy*c.InW+ix)*c.InC+o/mul]) - int32(c.InZero)
							acc += x * (int32(c.Weights[(ky*c.KW+kx)*c.OutC+o]) - int32(c.WZero))
							continue
						}
						for i := 0; i < c.InC; i++ {
							x := int32(in[(iy*c.InW+ix)*c.InC+i]) - int32(c.InZero)
							acc += x * (int32(c.Weights[((o*c.KH+ky)*c.KW+kx)*c.InC+i]) - int32(c.WZero))
						}
					}
				}
				v := int32(c.OutZero) + roundingDivPOT(highMul(acc, mult), shift)
				out[(oy*ow+ox)*c.OutC+o] = uint8(min(max(v, int32(lo)), int32(hi)))
			}
		}
	}
	return out
}

// gemmlowp's SaturatingRoundingDoublingHighMul and RoundingDivideByPOT
func highMul(a, b int32) int32 {
	if a == math.MinInt32 && b == math.MinInt32 {
		return math.MaxInt32
	}
	ab := int64(a) * int64(b)
	nudge := int64(1 << 30)
	if ab < 0 {
		nudge = 1 - 1<<30
	}
	return int32((ab + nudge) / (1 << 31))
}

func roundingDivPOT(x, e int32) int32 {
	mask := int32(1)<<e - 1
	rem, th := x&mask, mask>>1
	if x < 0 {
		th++
	}
	r := x >> e
	if rem > th {
		r++
	}
	return r
}

// Conv runs a prepared conv on in (NHWC uint8, at offset inOff of the data area), writing out (at
// outOff); the coefficients and bias go at constOff.
func (d *Device) Conv(pr *Prepared, inOff, inSize, outOff, outSize, constOff uint32, timeoutMs int) error {
	data := d.Data()
	coefOff := constOff
	biasOff := align(coefOff+uint32(len(pr.Coef)), 64)
	if int(biasOff)+len(pr.Bias) > len(data) {
		return errors.New("vpu: the constants do not fit")
	}
	copy(data[coefOff:], pr.Coef)
	copy(data[biasOff:], pr.Bias)
	out, err := d.Command([]Op{{Type: pr.Type, Params: pr.Params, In: []int{0, 1, 2}, Out: []int{3}}}, []Tensor{
		{Offset: inOff, Size: inSize},
		{Offset: coefOff, Size: uint32(len(pr.Coef))},
		{Offset: biasOff, Size: uint32(len(pr.Bias))},
		{Offset: outOff, Size: outSize, Write: true},
	}, 4, timeoutMs)
	if err != nil {
		return err
	}
	if s := binary.LittleEndian.Uint32(out); s != 0 {
		return fmt.Errorf("vpu: conv: status %#x", s)
	}
	return nil
}
