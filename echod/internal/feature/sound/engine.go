package sound

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"
	"unsafe"
)

const (
	tfFloat32   = 0
	tfInt32     = 2
	tfComplex64 = 8
	tfInt8      = 9

	opConv2D          = 3
	opDepthwiseConv2D = 4
	opDequantize      = 6
	opFullyConnected  = 9
	opLogistic        = 14
	opMul             = 18
	opReshape         = 22
	opPad             = 34
	opGather          = 36
	opMean            = 40
	opStridedSlice    = 45
	opSplit           = 49
	opLog             = 73
	opQuantize        = 114
	opRFFT2D          = 131
	opComplexAbs      = 135

	actNone  = 0
	actRelu  = 1
	actRelu1 = 2
	actRelu6 = 3
)

type engTensor struct {
	shape   []int
	elems   int
	ttype   uint8
	isConst bool
	raw     []byte // zero-copy slice into mmap for constants
	scales  []float32
	zp      int32

	// Dynamic storage (allocated only as needed; int8 activations share ping/pong)
	f32 []float32
	i8  []int8
}

type engOp struct {
	code      int32
	in        []int
	out       []int
	padding   uint8
	strideW   int
	strideH   int
	dilationW int
	dilationH int
	depthMul  int
	act       uint8

	effBias  []int32
	effScale []float32
	qmin     int32
	qmax     int32
	lut      [256]int8
}

// Engine executes yamnet.tflite in-place over a read-only mmap with < 500 KB of Go heap.
type Engine struct {
	raw      []byte
	unmap    func()
	tensors  []engTensor
	ops      []engOp
	inputIdx int
	outIdx   int

	// Ping-pong int8 activation buffers (98,304 bytes each = 192 KB total)
	pingI8 []int8
	pongI8 []int8

	// Precomputed 512-point RFFT tables (via 256-point radix-2 complex FFT)
	bitrev256 [256]uint16
	tw256Re   [128]float32
	tw256Im   [128]float32
	tw512Re   [256]float32
	tw512Im   [256]float32
	fftZr     [256]float32
	fftZi     [256]float32
}

func rdU16(b []byte, p int) uint16 { return binary.LittleEndian.Uint16(b[p:]) }
func rdU32(b []byte, p int) uint32 { return binary.LittleEndian.Uint32(b[p:]) }
func rdI32(b []byte, p int) int32  { return int32(binary.LittleEndian.Uint32(b[p:])) }
func rdI64(b []byte, p int) int64  { return int64(binary.LittleEndian.Uint64(b[p:])) }
func rdF32(b []byte, p int) float32 {
	return math.Float32frombits(binary.LittleEndian.Uint32(b[p:]))
}

func fbField(b []byte, tab int, slot int) int {
	vt := tab - int(rdI32(b, tab))
	vlen := int(rdU16(b, vt))
	off := 4 + 2*slot
	if off >= vlen {
		return 0
	}
	fo := int(rdU16(b, vt+off))
	if fo == 0 {
		return 0
	}
	return tab + fo
}

func fbIndirect(b []byte, p int) int { return p + int(rdU32(b, p)) }

func fbVecLen(b []byte, f int) int {
	if f == 0 {
		return 0
	}
	return int(rdU32(b, fbIndirect(b, f)))
}

func fbVecElem(b []byte, f, i, sz int) int {
	return fbIndirect(b, f) + 4 + i*sz
}

func roundToNearest(x float32) int32 {
	return int32(math.RoundToEven(float64(x)))
}

func clampI32(v, lo, hi int32) int32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func actBounds(act uint8, scale float32, zp int32) (int32, int32) {
	lo, hi := int32(-128), int32(127)
	switch act {
	case actRelu:
		if z := roundToNearest(0/scale) + zp; z > lo {
			lo = z
		}
	case actRelu1:
		if a := roundToNearest(-1/scale) + zp; a > lo {
			lo = a
		}
		if b := roundToNearest(1/scale) + zp; b < hi {
			hi = b
		}
	case actRelu6:
		if a := roundToNearest(0/scale) + zp; a > lo {
			lo = a
		}
		if b := roundToNearest(6/scale) + zp; b < hi {
			hi = b
		}
	}
	return lo, hi
}

func bytesAsInt8(b []byte) []int8 {
	if len(b) == 0 {
		return nil
	}
	return unsafe.Slice((*int8)(unsafe.Pointer(&b[0])), len(b))
}

// LoadEngine maps path read-only and prepares a zero-allocation YAMNet interpreter.
func LoadEngine(path string) (eng *Engine, err error) {
	data, unmap, err := mapFile(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if r := recover(); r != nil {
			unmap()
			eng = nil
			err = fmt.Errorf("sound: malformed tflite model: %v", r)
		} else if err != nil {
			unmap()
		}
	}()

	if len(data) < 64 {
		return nil, errors.New("sound: tflite model too short")
	}

	e := &Engine{raw: data, unmap: unmap}
	root := int(rdU32(data, 0))
	opcodesF := fbField(data, root, 1)
	subgraphsF := fbField(data, root, 2)
	buffersF := fbField(data, root, 4)

	if fbVecLen(data, subgraphsF) == 0 {
		return nil, errors.New("sound: tflite has no subgraphs")
	}

	opcodes := make([]int32, fbVecLen(data, opcodesF))
	for i := range opcodes {
		oc := fbIndirect(data, fbVecElem(data, opcodesF, i, 4))
		if extF := fbField(data, oc, 3); extF != 0 {
			opcodes[i] = rdI32(data, extF)
		} else if depF := fbField(data, oc, 0); depF != 0 {
			opcodes[i] = int32(data[depF])
		}
	}

	sg := fbIndirect(data, fbVecElem(data, subgraphsF, 0, 4))
	tensorsF := fbField(data, sg, 0)
	inputsF := fbField(data, sg, 1)
	outputsF := fbField(data, sg, 2)
	opsF := fbField(data, sg, 3)

	if fbVecLen(data, inputsF) < 1 || fbVecLen(data, outputsF) < 1 {
		return nil, errors.New("sound: missing subgraph inputs or outputs")
	}
	e.inputIdx = int(rdI32(data, fbVecElem(data, inputsF, 0, 4)))
	e.outIdx = int(rdI32(data, fbVecElem(data, outputsF, 0, 4)))

	maxI8 := 0
	e.tensors = make([]engTensor, fbVecLen(data, tensorsF))
	for i := range e.tensors {
		tt := fbIndirect(data, fbVecElem(data, tensorsF, i, 4))
		sf := fbField(data, tt, 0)
		tf := fbField(data, tt, 1)
		bf := fbField(data, tt, 2)
		qf := fbField(data, tt, 4)

		var t engTensor
		if tf != 0 {
			t.ttype = data[tf]
		}
		t.shape = make([]int, fbVecLen(data, sf))
		t.elems = 1
		for d := range t.shape {
			t.shape[d] = int(rdI32(data, fbVecElem(data, sf, d, 4)))
			t.elems *= t.shape[d]
		}

		if bf != 0 {
			bidx := int(rdU32(data, bf))
			if bidx > 0 && bidx < fbVecLen(data, buffersF) {
				btab := fbIndirect(data, fbVecElem(data, buffersF, bidx, 4))
				df := fbField(data, btab, 0)
				if n := fbVecLen(data, df); n > 0 {
					dp := fbIndirect(data, df) + 4
					t.isConst = true
					t.raw = data[dp : dp+n]
					if t.ttype == tfInt8 {
						t.i8 = bytesAsInt8(t.raw)
					}
				}
			}
		}

		if !t.isConst {
			switch t.ttype {
			case tfFloat32:
				t.f32 = make([]float32, t.elems)
			case tfComplex64:
				t.f32 = make([]float32, t.elems*2)
			case tfInt8:
				if t.elems > maxI8 {
					maxI8 = t.elems
				}
			}
		}

		if qf != 0 {
			qt := fbIndirect(data, qf)
			scf := fbField(data, qt, 2)
			zpf := fbField(data, qt, 3)
			if n := fbVecLen(data, scf); n > 0 {
				t.scales = make([]float32, n)
				for j := 0; j < n; j++ {
					t.scales[j] = rdF32(data, fbVecElem(data, scf, j, 4))
				}
			}
			if fbVecLen(data, zpf) > 0 {
				t.zp = int32(rdI64(data, fbVecElem(data, zpf, 0, 8)))
			}
		}
		e.tensors[i] = t
	}

	if e.tensors[e.inputIdx].elems != windowSamples {
		return nil, fmt.Errorf("sound: expected %d input samples, got %d", windowSamples, e.tensors[e.inputIdx].elems)
	}
	if e.tensors[e.outIdx].elems != len(audioSetClasses) {
		return nil, fmt.Errorf("sound: expected %d output classes, got %d", len(audioSetClasses), e.tensors[e.outIdx].elems)
	}

	if maxI8 > 0 {
		e.pingI8 = make([]int8, maxI8)
		e.pongI8 = make([]int8, maxI8)
	}

	e.ops = make([]engOp, fbVecLen(data, opsF))
	for i := range e.ops {
		ot := fbIndirect(data, fbVecElem(data, opsF, i, 4))
		ocf := fbField(data, ot, 0)
		inf := fbField(data, ot, 1)
		outf := fbField(data, ot, 2)
		boptf := fbField(data, ot, 4)

		ocIdx := 0
		if ocf != 0 {
			ocIdx = int(rdU32(data, ocf))
		}
		op := engOp{
			code:      opcodes[ocIdx],
			in:        make([]int, fbVecLen(data, inf)),
			out:       make([]int, fbVecLen(data, outf)),
			strideW:   1,
			strideH:   1,
			dilationW: 1,
			dilationH: 1,
			depthMul:  1,
		}
		for j := range op.in {
			op.in[j] = int(rdI32(data, fbVecElem(data, inf, j, 4)))
		}
		for j := range op.out {
			op.out[j] = int(rdI32(data, fbVecElem(data, outf, j, 4)))
		}

		if boptf != 0 {
			bopt := fbIndirect(data, boptf)
			switch op.code {
			case opConv2D:
				if s := fbField(data, bopt, 0); s != 0 {
					op.padding = data[s]
				}
				if s := fbField(data, bopt, 1); s != 0 {
					op.strideW = int(rdI32(data, s))
				}
				if s := fbField(data, bopt, 2); s != 0 {
					op.strideH = int(rdI32(data, s))
				}
				if s := fbField(data, bopt, 3); s != 0 {
					op.act = data[s]
				}
				if s := fbField(data, bopt, 4); s != 0 {
					op.dilationW = int(rdI32(data, s))
				}
				if s := fbField(data, bopt, 5); s != 0 {
					op.dilationH = int(rdI32(data, s))
				}
			case opDepthwiseConv2D:
				if s := fbField(data, bopt, 0); s != 0 {
					op.padding = data[s]
				}
				if s := fbField(data, bopt, 1); s != 0 {
					op.strideW = int(rdI32(data, s))
				}
				if s := fbField(data, bopt, 2); s != 0 {
					op.strideH = int(rdI32(data, s))
				}
				if s := fbField(data, bopt, 3); s != 0 {
					op.depthMul = int(rdI32(data, s))
				}
				if s := fbField(data, bopt, 4); s != 0 {
					op.act = data[s]
				}
				if s := fbField(data, bopt, 5); s != 0 {
					op.dilationW = int(rdI32(data, s))
				}
				if s := fbField(data, bopt, 6); s != 0 {
					op.dilationH = int(rdI32(data, s))
				}
			case opMul, opFullyConnected:
				if s := fbField(data, bopt, 0); s != 0 {
					op.act = data[s]
				}
			}
		}

		// Precompute quantized tables once at load time.
		if (op.code == opConv2D || op.code == opDepthwiseConv2D || op.code == opFullyConnected) &&
			e.tensors[op.in[0]].ttype == tfInt8 {
			tin := &e.tensors[op.in[0]]
			tw := &e.tensors[op.in[1]]
			var tb *engTensor
			if len(op.in) > 2 && op.in[2] >= 0 {
				tb = &e.tensors[op.in[2]]
			}
			tout := &e.tensors[op.out[0]]

			ocCnt := tw.shape[0]
			if op.code == opDepthwiseConv2D {
				ocCnt = tw.shape[3]
			}
			kElems := tw.elems / ocCnt
			op.effScale = make([]float32, ocCnt)
			op.effBias = make([]int32, ocCnt)

			inSc := float64(tin.scales[0])
			outSc := float64(tout.scales[0])
			for c := 0; c < ocCnt; c++ {
				wSc := float64(tw.scales[0])
				if len(tw.scales) > 1 {
					wSc = float64(tw.scales[c])
				}
				op.effScale[c] = float32((inSc * wSc) / outSc)

				var bval int32
				if tb != nil && len(tb.raw) >= 4*(c+1) {
					bval = rdI32(tb.raw, 4*c)
				}
				if op.code != opDepthwiseConv2D {
					var wsum int32
					wc := tw.i8[c*kElems : (c+1)*kElems]
					for _, wv := range wc {
						wsum += int32(wv)
					}
					op.effBias[c] = bval - tin.zp*wsum
				} else {
					op.effBias[c] = bval
				}
			}
			op.qmin, op.qmax = actBounds(op.act, tout.scales[0], tout.zp)
		} else if op.code == opLogistic && e.tensors[op.in[0]].ttype == tfInt8 {
			tin := &e.tensors[op.in[0]]
			tout := &e.tensors[op.out[0]]
			for q := -128; q <= 127; q++ {
				x := float64(int32(q)-tin.zp) * float64(tin.scales[0])
				y := 1.0 / (1.0 + math.Exp(-x))
				oq := clampI32(roundToNearest(float32(y)/tout.scales[0])+tout.zp, -128, 127)
				op.lut[uint8(int8(q))] = int8(oq)
			}
		}

		e.ops[i] = op
	}

	e.initRFFT512()
	return e, nil
}

// Close releases the read-only mmap and all scratch buffers.
func (e *Engine) Close() {
	if e == nil {
		return
	}
	if e.unmap != nil {
		e.unmap()
		e.unmap = nil
	}
	e.raw = nil
	e.tensors = nil
	e.ops = nil
	e.pingI8 = nil
	e.pongI8 = nil
}

func (e *Engine) initRFFT512() {
	for i := 0; i < 256; i++ {
		var r uint16
		for b := 0; b < 8; b++ {
			r = (r << 1) | uint16((i>>b)&1)
		}
		e.bitrev256[i] = r
	}
	for k := 0; k < 128; k++ {
		ang := -2.0 * math.Pi * float64(k) / 256.0
		e.tw256Re[k] = float32(math.Cos(ang))
		e.tw256Im[k] = float32(math.Sin(ang))
	}
	for k := 0; k < 256; k++ {
		ang := -2.0 * math.Pi * float64(k) / 512.0
		e.tw512Re[k] = float32(math.Cos(ang))
		e.tw512Im[k] = float32(math.Sin(ang))
	}
}

func (e *Engine) rfft512Frame(in512, outCplx257 []float32) {
	zr, zi := &e.fftZr, &e.fftZi
	for i := 0; i < 256; i++ {
		j := int(e.bitrev256[i])
		zr[i] = in512[2*j]
		zi[i] = in512[2*j+1]
	}

	for length := 2; length <= 256; length <<= 1 {
		half := length >> 1
		step := 256 / length
		for i := 0; i < 256; i += length {
			for k := 0; k < half; k++ {
				wr := e.tw256Re[k*step]
				wi := e.tw256Im[k*step]
				u := i + k
				v := u + half
				tr := wr*zr[v] - wi*zi[v]
				ti := wr*zi[v] + wi*zr[v]
				zr[v] = zr[u] - tr
				zi[v] = zi[u] - ti
				zr[u] += tr
				zi[u] += ti
			}
		}
	}

	outCplx257[0] = zr[0] + zi[0]
	outCplx257[1] = 0
	outCplx257[2*256] = zr[0] - zi[0]
	outCplx257[2*256+1] = 0

	for k := 1; k < 256; k++ {
		mk := 256 - k
		er := 0.5 * (zr[k] + zr[mk])
		ei := 0.5 * (zi[k] - zi[mk])
		or_ := 0.5 * (zi[k] + zi[mk])
		oi := -0.5 * (zr[k] - zr[mk])
		wr := e.tw512Re[k]
		wi := e.tw512Im[k]
		outCplx257[2*k] = er + (wr*or_ - wi*oi)
		outCplx257[2*k+1] = ei + (wr*oi + wi*or_)
	}
}

// Score runs YAMNet on 15,600 16 kHz PCM samples and returns the top-8 AudioSet classes.
func (e *Engine) Score(pcm []int16) (EngineReply, error) {
	if len(pcm) < windowSamples {
		return EngineReply{}, errors.New("sound: short PCM window")
	}
	inF32 := e.tensors[e.inputIdx].f32
	var sumSq float64
	const scale = 1.0 / 32768.0
	for i := 0; i < windowSamples; i++ {
		v := float32(pcm[i]) * scale
		inF32[i] = v
		sumSq += float64(v) * float64(v)
	}
	rms := math.Sqrt(sumSq / windowSamples)
	dbfs := -120.0
	if rms > 1e-9 {
		dbfs = 20.0 * math.Log10(rms)
	}

	start := time.Now()
	if err := e.invoke(); err != nil {
		return EngineReply{}, err
	}
	ms := float64(time.Since(start).Microseconds()) / 1000.0

	scores := e.tensors[e.outIdx].f32
	const k = 8
	var top [k]TopClass
	for i := range top {
		top[i].ID = -1
		top[i].Score = -1
	}
	for id, sc := range scores {
		s := float64(sc)
		if s <= top[k-1].Score {
			continue
		}
		pos := k - 1
		for pos > 0 && s > top[pos-1].Score {
			top[pos] = top[pos-1]
			pos--
		}
		top[pos] = TopClass{ID: id, Name: audioSetClasses[id], Score: s}
	}

	return EngineReply{
		MS:      ms,
		RMSDBFS: dbfs,
		Top:     top[:],
	}, nil
}

func (e *Engine) allocI8(dst *engTensor, avoid []int8) {
	if len(avoid) > 0 && &avoid[0] == &e.pingI8[0] {
		dst.i8 = e.pongI8[:dst.elems]
	} else {
		dst.i8 = e.pingI8[:dst.elems]
	}
}

func (e *Engine) invoke() error {
	for idx := range e.ops {
		op := &e.ops[idx]
		t0 := &e.tensors[op.in[0]]
		tout := &e.tensors[op.out[0]]

		switch op.code {
		case opStridedSlice:
			begin := int(rdI32(e.tensors[op.in[1]].raw, 0))
			end := int(rdI32(e.tensors[op.in[2]].raw, 0))
			stride := int(rdI32(e.tensors[op.in[3]].raw, 0))
			for i, o := begin, 0; i < end && o < tout.elems; i, o = i+stride, o+1 {
				tout.f32[o] = t0.f32[i]
			}

		case opReshape:
			if tout.ttype == tfInt8 {
				tout.i8 = t0.i8
			} else {
				copy(tout.f32, t0.f32)
			}

		case opGather:
			tidx := &e.tensors[op.in[1]]
			sliceElems := t0.elems / t0.shape[0]
			for j := 0; j < tidx.elems; j++ {
				gi := int(rdI32(tidx.raw, 4*j))
				copy(tout.f32[j*sliceElems:(j+1)*sliceElems], t0.f32[gi*sliceElems:(gi+1)*sliceElems])
			}

		case opMul:
			t1 := &e.tensors[op.in[1]]
			bLen := t1.elems
			for j := 0; j < tout.elems; j++ {
				tout.f32[j] = t0.f32[j] * rdF32(t1.raw, 4*(j%bLen))
			}

		case opPad:
			pbuf := e.tensors[op.in[1]].raw
			pt := int(rdI32(pbuf, 0))
			pl := int(rdI32(pbuf, 8))
			h, w := t0.shape[0], t0.shape[1]
			ow := tout.shape[1]
			clear(tout.f32)
			for r := 0; r < h; r++ {
				dstOff := (r+pt)*ow + pl
				copy(tout.f32[dstOff:dstOff+w], t0.f32[r*w:(r+1)*w])
			}

		case opRFFT2D:
			frames := t0.elems / 512
			for f := 0; f < frames; f++ {
				e.rfft512Frame(t0.f32[f*512:(f+1)*512], tout.f32[f*514:(f+1)*514])
			}

		case opComplexAbs:
			for j := 0; j < tout.elems; j++ {
				re := t0.f32[2*j]
				im := t0.f32[2*j+1]
				tout.f32[j] = float32(math.Sqrt(float64(re*re + im*im)))
			}

		case opFullyConnected:
			tw := &e.tensors[op.in[1]]
			var tb *engTensor
			if len(op.in) > 2 && op.in[2] >= 0 {
				tb = &e.tensors[op.in[2]]
			}
			if t0.ttype == tfFloat32 {
				execFCF32(t0, tw, tb, tout)
			} else {
				e.allocI8(tout, t0.i8)
				execFCInt8(op, t0, tw, tout)
			}

		case opLog:
			for j := 0; j < tout.elems; j++ {
				tout.f32[j] = float32(math.Log(float64(t0.f32[j])))
			}

		case opQuantize:
			e.allocI8(tout, nil)
			invSc := 1.0 / tout.scales[0]
			zp := tout.zp
			for j := 0; j < tout.elems; j++ {
				q := roundToNearest(t0.f32[j]*invSc) + zp
				tout.i8[j] = int8(clampI32(q, -128, 127))
			}

		case opSplit:
			tval := &e.tensors[op.in[1]]
			tout.i8 = tval.i8

		case opConv2D:
			tw := &e.tensors[op.in[1]]
			var tb *engTensor
			if len(op.in) > 2 && op.in[2] >= 0 {
				tb = &e.tensors[op.in[2]]
			}
			e.allocI8(tout, t0.i8)
			execConv2DInt8(op, t0, tw, tb, tout)

		case opDepthwiseConv2D:
			tw := &e.tensors[op.in[1]]
			e.allocI8(tout, t0.i8)
			execDWConv2DInt8(op, t0, tw, tout)

		case opMean:
			e.allocI8(tout, t0.i8)
			hw := t0.shape[1] * t0.shape[2]
			ch := t0.shape[3]
			rescale := t0.scales[0] / (tout.scales[0] * float32(hw))
			inZP, outZP := t0.zp, tout.zp
			for c := 0; c < ch; c++ {
				var sum int32
				for p := 0; p < hw; p++ {
					sum += int32(t0.i8[p*ch+c]) - inZP
				}
				q := roundToNearest(float32(sum)*rescale) + outZP
				tout.i8[c] = int8(clampI32(q, -128, 127))
			}

		case opLogistic:
			e.allocI8(tout, t0.i8)
			for j := 0; j < tout.elems; j++ {
				tout.i8[j] = op.lut[uint8(t0.i8[j])]
			}

		case opDequantize:
			sc, zp := t0.scales[0], t0.zp
			for j := 0; j < tout.elems; j++ {
				tout.f32[j] = float32(int32(t0.i8[j])-zp) * sc
			}

		default:
			return fmt.Errorf("sound: unsupported opcode %d at op %d", op.code, idx)
		}
	}
	return nil
}

func execConv2D1x1Int8(op *engOp, tin, tw, tout *engTensor) {
	pixels := tin.shape[0] * tin.shape[1] * tin.shape[2]
	icCnt := tin.shape[3]
	ocCnt := tw.shape[0]
	inData := tin.i8
	wData := tw.i8
	outData := tout.i8
	outZP := tout.zp
	qmin, qmax := op.qmin, op.qmax
	bias := op.effBias
	scale := op.effScale

	for p := 0; p < pixels; p++ {
		inPx := inData[p*icCnt : (p+1)*icCnt]
		outPx := outData[p*ocCnt : (p+1)*ocCnt]
		oc := 0
		for ; oc+4 <= ocCnt; oc += 4 {
			acc := [4]int32{bias[oc], bias[oc+1], bias[oc+2], bias[oc+3]}
			w0 := wData[(oc+0)*icCnt : (oc+1)*icCnt]
			w1 := wData[(oc+1)*icCnt : (oc+2)*icCnt]
			w2 := wData[(oc+2)*icCnt : (oc+3)*icCnt]
			w3 := wData[(oc+3)*icCnt : (oc+4)*icCnt]
			dot4I8(inPx, w0, w1, w2, w3, &acc)
			for lane := 0; lane < 4; lane++ {
				q := roundToNearest(float32(acc[lane])*scale[oc+lane]) + outZP
				outPx[oc+lane] = int8(clampI32(q, qmin, qmax))
			}
		}
		for ; oc < ocCnt; oc++ {
			a := bias[oc]
			wc := wData[oc*icCnt : (oc+1)*icCnt]
			for ic := 0; ic < icCnt; ic++ {
				a += int32(inPx[ic]) * int32(wc[ic])
			}
			q := roundToNearest(float32(a)*scale[oc]) + outZP
			outPx[oc] = int8(clampI32(q, qmin, qmax))
		}
	}
}

func execConv2DInt8(op *engOp, tin, tw, tb, tout *engTensor) {
	inH, inW, icCnt := tin.shape[1], tin.shape[2], tin.shape[3]
	ocCnt, kh, kw := tw.shape[0], tw.shape[1], tw.shape[2]
	outH, outW := tout.shape[1], tout.shape[2]
	sh, sw := op.strideH, op.strideW

	if kh == 1 && kw == 1 && sh == 1 && sw == 1 && op.dilationH == 1 && op.dilationW == 1 {
		execConv2D1x1Int8(op, tin, tw, tout)
		return
	}

	padTop, padLeft := 0, 0
	if op.padding == 0 {
		if padH := (outH-1)*sh + kh - inH; padH > 0 {
			padTop = padH / 2
		}
		if padW := (outW-1)*sw + kw - inW; padW > 0 {
			padLeft = padW / 2
		}
	}

	inData, wData, outData := tin.i8, tw.i8, tout.i8
	inZP, outZP := tin.zp, tout.zp
	qmin, qmax := op.qmin, op.qmax
	filterStride := kh * kw * icCnt

	for oh := 0; oh < outH; oh++ {
		for ow := 0; ow < outW; ow++ {
			outOff := (oh*outW + ow) * ocCnt
			for oc := 0; oc < ocCnt; oc++ {
				var acc int32
				if tb != nil && len(tb.raw) >= 4*(oc+1) {
					acc = rdI32(tb.raw, 4*oc)
				}
				wc := wData[oc*filterStride : (oc+1)*filterStride]
				for fh := 0; fh < kh; fh++ {
					ih := oh*sh - padTop + fh
					if ih < 0 || ih >= inH {
						continue
					}
					for fw := 0; fw < kw; fw++ {
						iw := ow*sw - padLeft + fw
						if iw < 0 || iw >= inW {
							continue
						}
						inOff := (ih*inW + iw) * icCnt
						wfOff := (fh*kw + fw) * icCnt
						for ic := 0; ic < icCnt; ic++ {
							acc += (int32(inData[inOff+ic]) - inZP) * int32(wc[wfOff+ic])
						}
					}
				}
				q := roundToNearest(float32(acc)*op.effScale[oc]) + outZP
				outData[outOff+oc] = int8(clampI32(q, qmin, qmax))
			}
		}
	}
}

func execDWConv2DInt8(op *engOp, tin, tw, tout *engTensor) {
	inH, inW, ch := tin.shape[1], tin.shape[2], tin.shape[3]
	kh, kw := tw.shape[1], tw.shape[2]
	outH, outW := tout.shape[1], tout.shape[2]
	sh, sw := op.strideH, op.strideW

	padTop, padLeft := 0, 0
	if op.padding == 0 {
		if padH := (outH-1)*sh + kh - inH; padH > 0 {
			padTop = padH / 2
		}
		if padW := (outW-1)*sw + kw - inW; padW > 0 {
			padLeft = padW / 2
		}
	}

	inData, wData, outData := tin.i8, tw.i8, tout.i8
	inZP, outZP := tin.zp, tout.zp
	qmin, qmax := op.qmin, op.qmax
	bias, scale := op.effBias, op.effScale
	accBuf := make([]int32, ch)

	for oh := 0; oh < outH; oh++ {
		for ow := 0; ow < outW; ow++ {
			copy(accBuf, bias)
			for fh := 0; fh < kh; fh++ {
				ih := oh*sh - padTop + fh
				if ih < 0 || ih >= inH {
					continue
				}
				for fw := 0; fw < kw; fw++ {
					iw := ow*sw - padLeft + fw
					if iw < 0 || iw >= inW {
						continue
					}
					ip := inData[(ih*inW+iw)*ch : (ih*inW+iw+1)*ch]
					wp := wData[(fh*kw+fw)*ch : (fh*kw+fw+1)*ch]
					_ = wp[ch-1]
					for c := 0; c < ch; c++ {
						accBuf[c] += (int32(ip[c]) - inZP) * int32(wp[c])
					}
				}
			}
			outPx := outData[(oh*outW+ow)*ch : (oh*outW+ow+1)*ch]
			for c := 0; c < ch; c++ {
				q := roundToNearest(float32(accBuf[c])*scale[c]) + outZP
				outPx[c] = int8(clampI32(q, qmin, qmax))
			}
		}
	}
}

func execFCF32(tin, tw, tb, tout *engTensor) {
	kCnt := tw.shape[1]
	ocCnt := tw.shape[0]
	rows := tin.elems / kCnt
	for r := 0; r < rows; r++ {
		inR := tin.f32[r*kCnt : (r+1)*kCnt]
		outR := tout.f32[r*ocCnt : (r+1)*ocCnt]
		for oc := 0; oc < ocCnt; oc++ {
			var sum float32
			if tb != nil && len(tb.raw) >= 4*(oc+1) {
				sum = rdF32(tb.raw, 4*oc)
			}
			wBase := oc * kCnt * 4
			for k := 0; k < kCnt; k++ {
				sum += inR[k] * rdF32(tw.raw, wBase+4*k)
			}
			outR[oc] = sum
		}
	}
}

func execFCInt8(op *engOp, tin, tw, tout *engTensor) {
	kCnt := tw.shape[1]
	ocCnt := tw.shape[0]
	rows := tin.elems / kCnt
	inData, wData, outData := tin.i8, tw.i8, tout.i8
	outZP := tout.zp
	qmin, qmax := op.qmin, op.qmax
	bias, scale := op.effBias, op.effScale

	for r := 0; r < rows; r++ {
		inR := inData[r*kCnt : (r+1)*kCnt]
		outR := outData[r*ocCnt : (r+1)*ocCnt]
		oc := 0
		for ; oc+4 <= ocCnt; oc += 4 {
			acc := [4]int32{bias[oc], bias[oc+1], bias[oc+2], bias[oc+3]}
			w0 := wData[(oc+0)*kCnt : (oc+1)*kCnt]
			w1 := wData[(oc+1)*kCnt : (oc+2)*kCnt]
			w2 := wData[(oc+2)*kCnt : (oc+3)*kCnt]
			w3 := wData[(oc+3)*kCnt : (oc+4)*kCnt]
			dot4I8(inR, w0, w1, w2, w3, &acc)
			for lane := 0; lane < 4; lane++ {
				q := roundToNearest(float32(acc[lane])*scale[oc+lane]) + outZP
				outR[oc+lane] = int8(clampI32(q, qmin, qmax))
			}
		}
		for ; oc < ocCnt; oc++ {
			a := bias[oc]
			wc := wData[oc*kCnt : (oc+1)*kCnt]
			for k := 0; k < kCnt; k++ {
				a += int32(inR[k]) * int32(wc[k])
			}
			q := roundToNearest(float32(a)*scale[oc]) + outZP
			outR[oc] = int8(clampI32(q, qmin, qmax))
		}
	}
}
