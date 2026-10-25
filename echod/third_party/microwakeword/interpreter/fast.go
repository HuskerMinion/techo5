package interpreter

// techo5: the hot kernels, rewritten for a small ARM core (the wake word runs on every 10 ms of
// audio on Echo Show units with four Cortex-A53s). The originals are in ops_ref_test.go; fast_test.go
// checks these give the same bytes. What changed:
//
//   - everything that depends only on the model (geometry, padding, quantized multipliers, the
//     filter and bias widened to int32) is worked out once per operator and kept in a plan;
//   - the input is widened, zero point taken off, once per call instead of once per use;
//   - Conv2D's inner loops are dot products over contiguous memory: with no dilation along the
//     width, a filter row and the input it covers are both contiguous (fw, ic) runs;
//   - DepthwiseConv2D accumulates all channels of a pixel at once, walking memory in order;
//   - StridedSlice copies contiguous runs, worked out once, instead of element by element.
//
// The integer arithmetic is the same (int32 sums wrap the same in any order), so the outputs are
// exact.

import (
	"fmt"

	"github.com/zserge/microwakeword/model"
)

// plan is the operator's cached plan, built on its first run, keyed by subgraph and operator: an init
// subgraph's operators (opCallOnce) count from zero as the main graph's do.
func (interp *Interpreter) plan(build func() (any, error)) (any, error) {
	key := interp.sgIdx<<16 | interp.opIdx
	if p, ok := interp.plans[key]; ok {
		return p, nil
	}
	p, err := build()
	if err != nil {
		return nil, err
	}
	if interp.plans == nil {
		interp.plans = make(map[int]any)
	}
	interp.plans[key] = p
	return p, nil
}

func optInt(op model.Operator, name string, def int) int {
	if v, ok := op.Options[name]; ok {
		return v.(int)
	}
	return def
}

// dot is the sum of x[i]*w[i]; w is at least as long as x.
func dot(x, w []int32) int32 {
	w = w[:len(x)]
	var a0, a1, a2, a3 int32
	i := 0
	for ; i+4 <= len(x); i += 4 {
		a0 += x[i] * w[i]
		a1 += x[i+1] * w[i+1]
		a2 += x[i+2] * w[i+2]
		a3 += x[i+3] * w[i+3]
	}
	for ; i < len(x); i++ {
		a0 += x[i] * w[i]
	}
	return a0 + a1 + a2 + a3
}

type convGeom struct {
	batch, inH, inW, inC, outH, outW, outC, fH, fW int
	strideH, strideW, dilH, dilW, padH, padW       int
	inZP, outZP, actMin, actMax                    int32
	qmuls                                          []QuantMultiplier
	filt, bias, in32                               []int32
}

func newConvGeom(interp *Interpreter, op model.Operator, depthwise bool) (*convGeom, error) {
	if len(op.Inputs) < 3 || len(op.Outputs) != 1 {
		return nil, fmt.Errorf("bad io")
	}
	sg := interp.model.Subgraphs[interp.sgIdx]
	inT := sg.Tensors[op.Inputs[0]]
	filterT := sg.Tensors[op.Inputs[1]]
	outT := sg.Tensors[op.Outputs[0]]
	g := &convGeom{
		batch: inT.Shape[0], inH: inT.Shape[1], inW: inT.Shape[2], inC: inT.Shape[3],
		outH: outT.Shape[1], outW: outT.Shape[2],
		fH: filterT.Shape[1], fW: filterT.Shape[2],
		strideH: optInt(op, "stride_h", 1), strideW: optInt(op, "stride_w", 1),
		dilH: optInt(op, "dilation_h", 1), dilW: optInt(op, "dilation_w", 1),
	}
	if depthwise {
		g.outC = g.inC * optInt(op, "depth_multiplier", 1)
	} else {
		g.outC = filterT.Shape[0]
	}
	if len(inT.Quant.ZeroPoint) > 0 {
		g.inZP = int32(inT.Quant.ZeroPoint[0])
	}
	if len(outT.Quant.ZeroPoint) > 0 {
		g.outZP = int32(outT.Quant.ZeroPoint[0])
	}
	inScale, outScale := float64(0), float64(0)
	if len(inT.Quant.Scale) > 0 {
		inScale = float64(inT.Quant.Scale[0])
	}
	if len(outT.Quant.Scale) > 0 {
		outScale = float64(outT.Quant.Scale[0])
	}
	if outScale == 0 {
		outScale = 1.0
	}
	g.qmuls = make([]QuantMultiplier, g.outC)
	for c := range g.qmuls {
		fs := float64(0)
		if c < len(filterT.Quant.Scale) {
			fs = float64(filterT.Quant.Scale[c])
		} else if len(filterT.Quant.Scale) > 0 {
			fs = float64(filterT.Quant.Scale[0])
		}
		g.qmuls[c] = QuantizeMultiplier(inScale * fs / outScale)
	}
	if optInt(op, "padding", 0) == model.PaddingSame {
		effFH := (g.fH-1)*g.dilH + 1
		effFW := (g.fW-1)*g.dilW + 1
		g.padH = max(0, ((g.outH-1)*g.strideH+effFH-g.inH)/2)
		g.padW = max(0, ((g.outW-1)*g.strideW+effFW-g.inW)/2)
	}
	g.actMin, g.actMax = activationBounds(optInt(op, "fused_activation", 0), g.outZP)

	fd := interp.tensors[op.Inputs[1]].data
	g.filt = make([]int32, len(fd))
	for i, v := range fd {
		g.filt[i] = int32(int8(v))
	}
	bd := interp.tensors[op.Inputs[2]].data
	g.bias = make([]int32, g.outC)
	for c := range g.bias {
		g.bias[c] = getBias32(bd, c)
	}
	g.in32 = make([]int32, g.batch*g.inH*g.inW*g.inC)
	return g, nil
}

func (g *convGeom) widen(in []byte) {
	x := g.in32
	in = in[:len(x)]
	for i, v := range in {
		x[i] = int32(int8(v)) - g.inZP
	}
}

func (g *convGeom) store(out []byte, off int, acc int32, oc int) {
	acc = MultiplyByQuantizedMultiplier(acc, g.qmuls[oc]) + g.outZP
	if acc < g.actMin {
		acc = g.actMin
	}
	if acc > g.actMax {
		acc = g.actMax
	}
	out[off] = byte(int8(ClampInt8(acc)))
}

// opConv2D: quantized 2D convolution. Input [batch, inH, inW, inC], filter [outC, fH, fW, inC].
func opConv2D(interp *Interpreter, op model.Operator) error {
	p, err := interp.plan(func() (any, error) { return newConvGeom(interp, op, false) })
	if err != nil {
		return fmt.Errorf("CONV_2D: %w", err)
	}
	g := p.(*convGeom)
	g.widen(interp.tensors[op.Inputs[0]].data)
	out := interp.tensors[op.Outputs[0]].data
	x, filt := g.in32, g.filt
	rowW := g.fW * g.inC
	for b := 0; b < g.batch; b++ {
		for oh := 0; oh < g.outH; oh++ {
			for ow := 0; ow < g.outW; ow++ {
				iw0 := ow*g.strideW - g.padW
				// the filter columns that land inside the input (dilation 1: contiguous)
				fw0, fw1 := 0, g.fW
				if g.dilW == 1 {
					fw0 = max(0, -iw0)
					fw1 = min(g.fW, g.inW-iw0)
				}
				outOff := ((b*g.outH+oh)*g.outW + ow) * g.outC
				for oc := 0; oc < g.outC; oc++ {
					acc := g.bias[oc]
					for fh := 0; fh < g.fH; fh++ {
						ih := oh*g.strideH - g.padH + fh*g.dilH
						if ih < 0 || ih >= g.inH {
							continue
						}
						rowIn := ((b*g.inH + ih) * g.inW) * g.inC
						rowF := (oc*g.fH + fh) * rowW
						if g.dilW == 1 {
							if fw0 < fw1 {
								n := (fw1 - fw0) * g.inC
								s := rowIn + (iw0+fw0)*g.inC
								acc += dot(x[s:s+n], filt[rowF+fw0*g.inC:])
							}
							continue
						}
						for fw := 0; fw < g.fW; fw++ {
							iw := iw0 + fw*g.dilW
							if iw < 0 || iw >= g.inW {
								continue
							}
							s := rowIn + iw*g.inC
							acc += dot(x[s:s+g.inC], filt[rowF+fw*g.inC:])
						}
					}
					g.store(out, outOff+oc, acc, oc)
				}
			}
		}
	}
	return nil
}

// opDepthwiseConv2D: quantized depthwise convolution. Filter [1, fH, fW, outC], outC = inC*mult.
func opDepthwiseConv2D(interp *Interpreter, op model.Operator) error {
	p, err := interp.plan(func() (any, error) {
		g, err := newConvGeom(interp, op, true)
		if err != nil {
			return nil, err
		}
		return &dwPlan{convGeom: g, acc: make([]int32, g.outC)}, nil
	})
	if err != nil {
		return fmt.Errorf("DEPTHWISE_CONV_2D: %w", err)
	}
	d := p.(*dwPlan)
	g := d.convGeom
	g.widen(interp.tensors[op.Inputs[0]].data)
	out := interp.tensors[op.Outputs[0]].data
	x, filt, acc := g.in32, g.filt, d.acc
	mult := g.outC / g.inC
	for b := 0; b < g.batch; b++ {
		for oh := 0; oh < g.outH; oh++ {
			for ow := 0; ow < g.outW; ow++ {
				copy(acc, g.bias)
				for fh := 0; fh < g.fH; fh++ {
					ih := oh*g.strideH - g.padH + fh*g.dilH
					if ih < 0 || ih >= g.inH {
						continue
					}
					for fw := 0; fw < g.fW; fw++ {
						iw := ow*g.strideW - g.padW + fw*g.dilW
						if iw < 0 || iw >= g.inW {
							continue
						}
						px := x[((b*g.inH+ih)*g.inW+iw)*g.inC:]
						fr := filt[(fh*g.fW+fw)*g.outC:]
						fr = fr[:len(acc)]
						if mult == 1 {
							px = px[:len(acc)]
							for c := range acc {
								acc[c] += px[c] * fr[c]
							}
						} else {
							for c := range acc {
								acc[c] += px[c/mult] * fr[c]
							}
						}
					}
				}
				outOff := ((b*g.outH+oh)*g.outW + ow) * g.outC
				for c, a := range acc {
					g.store(out, outOff+c, a, c)
				}
			}
		}
	}
	return nil
}

type dwPlan struct {
	*convGeom
	acc []int32
}

// sliceRun is one contiguous copy of a strided slice.
type sliceRun struct{ in, out, n int }

// opStridedSlice: copies the slice in contiguous runs worked out once (elements are bytes, as in
// the original).
func opStridedSlice(interp *Interpreter, op model.Operator) error {
	p, err := interp.plan(func() (any, error) { return newSlicePlan(interp, op) })
	if err != nil {
		return fmt.Errorf("STRIDED_SLICE: %w", err)
	}
	in := interp.tensors[op.Inputs[0]].data
	out := interp.tensors[op.Outputs[0]].data
	for _, r := range p.([]sliceRun) {
		copy(out[r.out:r.out+r.n], in[r.in:r.in+r.n])
	}
	return nil
}

func newSlicePlan(interp *Interpreter, op model.Operator) ([]sliceRun, error) {
	if len(op.Inputs) < 4 || len(op.Outputs) != 1 {
		return nil, fmt.Errorf("bad io")
	}
	sg := interp.model.Subgraphs[interp.sgIdx]
	inShape := sg.Tensors[op.Inputs[0]].Shape
	outShape := sg.Tensors[op.Outputs[0]].Shape
	ndim := len(inShape)
	beginMask := optInt(op, "begin_mask", 0)
	endMask := optInt(op, "end_mask", 0)
	// begin and stride are read once, into the plan: they are constant tensors in microWakeWord's
	// models, and a model that computed them would need the plan rebuilt on every run.
	begin := make([]int, ndim)
	stride := make([]int, ndim)
	for d := 0; d < ndim; d++ {
		begin[d] = getInt32FromBytes(interp.tensors[op.Inputs[1]].data, d)
		stride[d] = getInt32FromBytes(interp.tensors[op.Inputs[3]].data, d)
		if beginMask&(1<<uint(d)) != 0 {
			begin[d] = 0
		}
		if begin[d] < 0 {
			begin[d] += inShape[d]
		}
		if stride[d] == 0 {
			stride[d] = 1
		}
	}
	_ = endMask // the output shape already says how far each dimension goes

	// The same walk as the original, merged into runs where consecutive outputs read consecutive
	// inputs.
	var runs []sliceRun
	outTotal := model.TensorSize(outShape)
	coord := make([]int, ndim)
	for i := 0; i < outTotal; i++ {
		inOff, inStride := 0, 1
		for d := ndim - 1; d >= 0; d-- {
			inOff += (begin[d] + coord[d]*stride[d]) * inStride
			inStride *= inShape[d]
		}
		if n := len(runs); n > 0 && runs[n-1].in+runs[n-1].n == inOff && runs[n-1].out+runs[n-1].n == i {
			runs[n-1].n++
		} else {
			runs = append(runs, sliceRun{in: inOff, out: i, n: 1})
		}
		for d := ndim - 1; d >= 0; d-- {
			coord[d]++
			if coord[d] < outShape[d] {
				break
			}
			coord[d] = 0
		}
	}
	return runs, nil
}
