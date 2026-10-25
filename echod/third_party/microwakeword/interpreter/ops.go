package interpreter

// ops.go — TFLite op kernel implementations for MicroWakeWord models.

import (
	"fmt"
	"math"

	"github.com/zserge/microwakeword/model"
)

// opFunc is the signature for all op implementations.
type opFunc func(interp *Interpreter, op model.Operator) error

// techo5: built once, not on every Invoke
var opDispatch = getOpDispatch()

func getOpDispatch() map[int]opFunc {
	return map[int]opFunc{
		model.OpCallOnce:        opCallOnce,
		model.OpVarHandle:       opVarHandle,
		model.OpReshape:         opReshape,
		model.OpReadVariable:    opReadVariable,
		model.OpAssignVariable:  opAssignVariable,
		model.OpConcatenation:   opConcatenation,
		model.OpStridedSlice:    opStridedSlice,
		model.OpConv2D:          opConv2D,
		model.OpDepthwiseConv2D: opDepthwiseConv2D,
		model.OpFullyConnected:  opFullyConnected,
		model.OpLogistic:        opLogistic,
		model.OpQuantize:        opQuantize,
		model.OpSplitV:          opSplitV,
		// techo5: residual and pooled models' operations (#190)
		model.OpAdd:           opAdd,
		model.OpAveragePool2D: opAveragePool2D,
		model.OpMaxPool2D:     opMaxPool2D,
	}
}

// --- Simple ops ---

func opCallOnce(interp *Interpreter, op model.Operator) error {
	if interp.callOnceExecuted {
		return nil
	}
	idx, ok := op.Options["init_subgraph_index"]
	if !ok {
		return fmt.Errorf("CALL_ONCE: missing init_subgraph_index")
	}
	sgIdx := idx.(int)
	if sgIdx < 0 || sgIdx >= len(interp.model.Subgraphs) {
		return fmt.Errorf("CALL_ONCE: invalid subgraph %d", sgIdx)
	}
	initSG := interp.model.Subgraphs[sgIdx]
	// Allocate tensors for the init subgraph
	initTensors := make([]tensorData, len(initSG.Tensors))
	for i, t := range initSG.Tensors {
		initTensors[i] = makeTensorData(t, interp.model)
	}
	// Execute init subgraph ops
	saved := interp.tensors
	savedSG := interp.sgIdx
	// techo5: and the operator index, which keys the plans fast.go keeps
	savedOp := interp.opIdx
	interp.tensors = initTensors
	interp.sgIdx = sgIdx
	dispatch := getOpDispatch()
	for i, initOp := range initSG.Operators {
		interp.opIdx = i
		fn, ok := dispatch[initOp.OpCode]
		if !ok {
			return fmt.Errorf("CALL_ONCE init: unsupported op %s", model.OpName(initOp.OpCode))
		}
		if err := fn(interp, initOp); err != nil {
			return fmt.Errorf("CALL_ONCE init op %s: %w", model.OpName(initOp.OpCode), err)
		}
	}
	interp.tensors = saved
	interp.sgIdx = savedSG
	interp.opIdx = savedOp
	interp.callOnceExecuted = true
	return nil
}

func opVarHandle(interp *Interpreter, op model.Operator) error {
	if len(op.Outputs) != 1 {
		return fmt.Errorf("VAR_HANDLE: expected 1 output, got %d", len(op.Outputs))
	}
	// Determine the shared resource name.
	// First try FlexBuffer custom options.
	sharedName := model.ExtractFlexBufferString(op.CustomOptions, "shared_name")
	if sharedName == "" {
		// Fallback: use the tensor name, normalized to strip trailing
		// digits from the last path component.
		// This handles "stream_5/states1" → "stream_5/states".
		sg := interp.model.Subgraphs[interp.sgIdx]
		sharedName = normalizeResourceName(sg.Tensors[op.Outputs[0]].Name)
	}
	outIdx := op.Outputs[0]
	interp.varHandleNames[interp.sgIdx*1000+outIdx] = sharedName
	return nil
}

// normalizeResourceName strips trailing digits from the last path component.
// "stream_5/states1" → "stream_5/states", "stream/states" → "stream/states"
func normalizeResourceName(name string) string {
	// Find the last '/' and strip trailing digits from the suffix.
	lastSlash := -1
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '/' {
			lastSlash = i
			break
		}
	}
	suffix := name[lastSlash+1:]
	// Strip trailing digits from suffix
	end := len(suffix)
	for end > 0 && suffix[end-1] >= '0' && suffix[end-1] <= '9' {
		end--
	}
	if end == 0 || end == len(suffix) {
		return name // No digits to strip or all digits
	}
	return name[:lastSlash+1] + suffix[:end]
}

func getResourceName(interp *Interpreter, tensorIdx int) string {
	name, ok := interp.varHandleNames[interp.sgIdx*1000+tensorIdx]
	if ok {
		return name
	}
	// Fallback to tensor name
	sg := interp.model.Subgraphs[interp.sgIdx]
	return sg.Tensors[tensorIdx].Name
}

func opReadVariable(interp *Interpreter, op model.Operator) error {
	if len(op.Inputs) != 1 || len(op.Outputs) != 1 {
		return fmt.Errorf("READ_VARIABLE: expected 1 input 1 output")
	}
	handleName := getResourceName(interp, op.Inputs[0])
	data, ok := interp.resources[handleName]
	if !ok {
		return fmt.Errorf("READ_VARIABLE: resource %q not found", handleName)
	}
	out := &interp.tensors[op.Outputs[0]]
	copy(out.data, data)
	return nil
}

func opAssignVariable(interp *Interpreter, op model.Operator) error {
	if len(op.Inputs) != 2 {
		return fmt.Errorf("ASSIGN_VARIABLE: expected 2 inputs")
	}
	handleName := getResourceName(interp, op.Inputs[0])
	src := interp.tensors[op.Inputs[1]].data
	// Store a copy in the resource variable store
	dst := make([]byte, len(src))
	copy(dst, src)
	interp.resources[handleName] = dst
	return nil
}

func opReshape(interp *Interpreter, op model.Operator) error {
	if len(op.Inputs) < 1 || len(op.Outputs) != 1 {
		return fmt.Errorf("RESHAPE: bad io counts")
	}
	in := &interp.tensors[op.Inputs[0]]
	out := &interp.tensors[op.Outputs[0]]
	// Just copy data; the shape is already set from the tensor definition.
	copy(out.data, in.data)
	return nil
}

func opConcatenation(interp *Interpreter, op model.Operator) error {
	if len(op.Outputs) != 1 {
		return fmt.Errorf("CONCATENATION: bad output count")
	}
	sg := interp.model.Subgraphs[interp.sgIdx]
	axis := 0
	if v, ok := op.Options["axis"]; ok {
		axis = v.(int)
	}
	outTensor := sg.Tensors[op.Outputs[0]]
	out := &interp.tensors[op.Outputs[0]]

	// Compute the concatenation along `axis`.
	outShape := outTensor.Shape
	ndim := len(outShape)
	if axis < 0 {
		axis += ndim
	}

	// outerSize = product of dims before axis
	outerSize := 1
	for i := 0; i < axis; i++ {
		outerSize *= outShape[i]
	}
	// innerSize = product of dims after axis
	innerSize := 1
	for i := axis + 1; i < ndim; i++ {
		innerSize *= outShape[i]
	}

	dstOff := 0
	for outer := 0; outer < outerSize; outer++ {
		for _, inIdx := range op.Inputs {
			inT := sg.Tensors[inIdx]
			inData := interp.tensors[inIdx].data
			axisLen := inT.Shape[axis]
			chunkSize := axisLen * innerSize
			srcOff := outer * chunkSize
			copy(out.data[dstOff:dstOff+chunkSize], inData[srcOff:srcOff+chunkSize])
			dstOff += chunkSize
		}
	}
	return nil
}

func getInt32FromBytes(data []byte, idx int) int {
	off := idx * 4
	if off+4 > len(data) {
		return 0
	}
	v := int32(data[off]) | int32(data[off+1])<<8 | int32(data[off+2])<<16 | int32(data[off+3])<<24
	return int(v)
}

// opFullyConnected implements quantized fully connected (dense) layer.
// Input: [batch, inputSize], Weights: [outputSize, inputSize], Bias: [outputSize]
func opFullyConnected(interp *Interpreter, op model.Operator) error {
	if len(op.Inputs) < 3 || len(op.Outputs) != 1 {
		return fmt.Errorf("FULLY_CONNECTED: bad io")
	}
	sg := interp.model.Subgraphs[interp.sgIdx]

	inT := sg.Tensors[op.Inputs[0]]
	weightT := sg.Tensors[op.Inputs[1]]
	outT := sg.Tensors[op.Outputs[0]]

	inData := interp.tensors[op.Inputs[0]].data
	weightData := interp.tensors[op.Inputs[1]].data
	biasData := interp.tensors[op.Inputs[2]].data
	outData := interp.tensors[op.Outputs[0]].data

	activation := 0
	if v, ok := op.Options["fused_activation"]; ok {
		activation = v.(int)
	}

	// For FC, input is flattened. Total input size from weights.
	outputSize := weightT.Shape[0]
	inputSize := weightT.Shape[1]
	batch := model.TensorSize(inT.Shape) / inputSize

	inputZP := int32(0)
	if len(inT.Quant.ZeroPoint) > 0 {
		inputZP = int32(inT.Quant.ZeroPoint[0])
	}
	outputZP := int32(0)
	if len(outT.Quant.ZeroPoint) > 0 {
		outputZP = int32(outT.Quant.ZeroPoint[0])
	}

	inScale := float64(0)
	if len(inT.Quant.Scale) > 0 {
		inScale = float64(inT.Quant.Scale[0])
	}
	outScale := float64(0)
	if len(outT.Quant.Scale) > 0 {
		outScale = float64(outT.Quant.Scale[0])
	}
	// FC may have per-channel or single scale
	qmuls := make([]QuantMultiplier, outputSize)
	for c := 0; c < outputSize; c++ {
		wScale := float64(0)
		if c < len(weightT.Quant.Scale) {
			wScale = float64(weightT.Quant.Scale[c])
		} else if len(weightT.Quant.Scale) > 0 {
			wScale = float64(weightT.Quant.Scale[0])
		}
		if outScale == 0 {
			outScale = 1.0
		}
		qmuls[c] = QuantizeMultiplier(inScale * wScale / outScale)
	}

	actMin, actMax := activationBounds(activation, outputZP)

	for b := 0; b < batch; b++ {
		for oc := 0; oc < outputSize; oc++ {
			acc := int32(getBias32(biasData, oc))
			for ic := 0; ic < inputSize; ic++ {
				inVal := int32(int8(inData[b*inputSize+ic])) - inputZP
				wVal := int32(int8(weightData[oc*inputSize+ic]))
				acc += inVal * wVal
			}
			acc = MultiplyByQuantizedMultiplier(acc, qmuls[oc])
			acc += outputZP
			if acc < actMin {
				acc = actMin
			}
			if acc > actMax {
				acc = actMax
			}
			outData[b*outputSize+oc] = byte(int8(ClampInt8(acc)))
		}
	}
	return nil
}

// opLogistic applies the logistic (sigmoid) function.
func opLogistic(interp *Interpreter, op model.Operator) error {
	if len(op.Inputs) != 1 || len(op.Outputs) != 1 {
		return fmt.Errorf("LOGISTIC: bad io")
	}
	sg := interp.model.Subgraphs[interp.sgIdx]
	inT := sg.Tensors[op.Inputs[0]]
	outT := sg.Tensors[op.Outputs[0]]
	inData := interp.tensors[op.Inputs[0]].data
	outData := interp.tensors[op.Outputs[0]].data

	n := model.TensorSize(inT.Shape)

	inScale := float64(0)
	inZP := int32(0)
	if len(inT.Quant.Scale) > 0 {
		inScale = float64(inT.Quant.Scale[0])
	}
	if len(inT.Quant.ZeroPoint) > 0 {
		inZP = int32(inT.Quant.ZeroPoint[0])
	}
	outScale := float64(0)
	outZP := int32(0)
	if len(outT.Quant.Scale) > 0 {
		outScale = float64(outT.Quant.Scale[0])
	}
	if len(outT.Quant.ZeroPoint) > 0 {
		outZP = int32(outT.Quant.ZeroPoint[0])
	}

	for i := 0; i < n; i++ {
		// Dequantize input
		realVal := float64(int32(int8(inData[i]))-inZP) * inScale
		// Logistic
		sig := 1.0 / (1.0 + math.Exp(-realVal))
		// Quantize output
		outVal := int32(math.Round(sig/outScale)) + outZP
		if outT.Type == model.TensorUint8 {
			outData[i] = byte(ClampUint8(outVal))
		} else {
			outData[i] = byte(int8(ClampInt8(outVal)))
		}
	}
	return nil
}

// opQuantize re-quantizes a tensor from one scale/zp to another.
func opQuantize(interp *Interpreter, op model.Operator) error {
	if len(op.Inputs) != 1 || len(op.Outputs) != 1 {
		return fmt.Errorf("QUANTIZE: bad io")
	}
	sg := interp.model.Subgraphs[interp.sgIdx]
	inT := sg.Tensors[op.Inputs[0]]
	outT := sg.Tensors[op.Outputs[0]]
	inData := interp.tensors[op.Inputs[0]].data
	outData := interp.tensors[op.Outputs[0]].data

	n := model.TensorSize(inT.Shape)

	inScale := float64(0)
	inZP := int32(0)
	if len(inT.Quant.Scale) > 0 {
		inScale = float64(inT.Quant.Scale[0])
	}
	if len(inT.Quant.ZeroPoint) > 0 {
		inZP = int32(inT.Quant.ZeroPoint[0])
	}
	outScale := float64(0)
	outZP := int32(0)
	if len(outT.Quant.Scale) > 0 {
		outScale = float64(outT.Quant.Scale[0])
	}
	if len(outT.Quant.ZeroPoint) > 0 {
		outZP = int32(outT.Quant.ZeroPoint[0])
	}

	for i := 0; i < n; i++ {
		var realVal float64
		if inT.Type == model.TensorUint8 {
			realVal = float64(int32(inData[i])-inZP) * inScale
		} else {
			realVal = float64(int32(int8(inData[i]))-inZP) * inScale
		}
		outVal := int32(math.Round(realVal/outScale)) + outZP
		if outT.Type == model.TensorUint8 {
			outData[i] = byte(ClampUint8(outVal))
		} else {
			outData[i] = byte(int8(ClampInt8(outVal)))
		}
	}
	return nil
}

// techo5: ADD, AVERAGE_POOL_2D and MAX_POOL_2D, which MixedNet models trained with residual
// connections or pooling use (#190). They follow TFLite Micro's integer kernels; testdata/add_pool.py
// makes a model of all three and TFLite's outputs for it (addpool_test.go).

// addPlan is what ADD works out once per operator: the broadcast strides, the multipliers and the
// output's bounds, and the coordinate it walks with, so a step allocates nothing.
type addPlan struct {
	shape          []int
	s1, s2, coord  []int
	n, len1, len2  int
	m1, m2, mo     QuantMultiplier
	zp1, zp2, zpo  int32
	actMin, actMax int32
}

// addLeftShift is the headroom both inputs get before they are rescaled to a common scale.
const addLeftShift = 20

// opAdd implements quantized element-wise addition with NumPy-style
// broadcasting, following TFLite Micro's integer ADD kernel.
func opAdd(interp *Interpreter, op model.Operator) error {
	if len(op.Inputs) != 2 || len(op.Outputs) != 1 {
		return fmt.Errorf("ADD: bad io")
	}
	sg := interp.model.Subgraphs[interp.sgIdx]
	pl, err := interp.plan(func() (any, error) { return newAddPlan(sg, op) })
	if err != nil {
		return err
	}
	p := pl.(*addPlan)

	in1 := interp.tensors[op.Inputs[0]].data
	in2 := interp.tensors[op.Inputs[1]].data
	out := interp.tensors[op.Outputs[0]].data
	if len(in1) < p.len1 || len(in2) < p.len2 || len(out) < p.n {
		return fmt.Errorf("ADD: tensor data shorter than shape")
	}

	coord := p.coord
	clear(coord)
	for i := range p.n {
		off1, off2 := 0, 0
		for d, c := range coord {
			off1 += c * p.s1[d]
			off2 += c * p.s2[d]
		}
		a := (int32(int8(in1[off1])) - p.zp1) << addLeftShift
		b := (int32(int8(in2[off2])) - p.zp2) << addLeftShift
		sum := MultiplyByQuantizedMultiplier(a, p.m1) + MultiplyByQuantizedMultiplier(b, p.m2)
		v := MultiplyByQuantizedMultiplier(sum, p.mo) + p.zpo
		out[i] = byte(int8(min(max(v, p.actMin), p.actMax)))
		for d := len(coord) - 1; d >= 0; d-- {
			coord[d]++
			if coord[d] < p.shape[d] {
				break
			}
			coord[d] = 0
		}
	}
	return nil
}

func newAddPlan(sg model.Subgraph, op model.Operator) (*addPlan, error) {
	in1T := sg.Tensors[op.Inputs[0]]
	in2T := sg.Tensors[op.Inputs[1]]
	outT := sg.Tensors[op.Outputs[0]]
	for _, t := range []model.Tensor{in1T, in2T, outT} {
		if t.Type != model.TensorInt8 {
			return nil, fmt.Errorf("ADD: only int8 is supported")
		}
		if len(t.Quant.Scale) == 0 || t.Quant.Scale[0] <= 0 {
			return nil, fmt.Errorf("ADD: missing quantization")
		}
	}
	s1, err := broadcastStrides(in1T.Shape, outT.Shape)
	if err != nil {
		return nil, fmt.Errorf("ADD: %w", err)
	}
	s2, err := broadcastStrides(in2T.Shape, outT.Shape)
	if err != nil {
		return nil, fmt.Errorf("ADD: %w", err)
	}

	// Both inputs are rescaled to a common scale of twice the larger input
	// scale, with 20 bits of headroom, then summed and rescaled to the output.
	sc1 := float64(in1T.Quant.Scale[0])
	sc2 := float64(in2T.Quant.Scale[0])
	so := float64(outT.Quant.Scale[0])
	twiceMax := 2 * math.Max(sc1, sc2)
	zpo := zeroPoint(outT)
	activation := 0
	if v, ok := op.Options["fused_activation"]; ok {
		activation = v.(int)
	}
	actMin, actMax := activationBounds(activation, zpo)
	return &addPlan{
		shape: outT.Shape,
		s1:    s1, s2: s2, coord: make([]int, len(outT.Shape)),
		n:    numElems(outT.Shape),
		len1: numElems(in1T.Shape), len2: numElems(in2T.Shape),
		m1:  QuantizeMultiplier(sc1 / twiceMax),
		m2:  QuantizeMultiplier(sc2 / twiceMax),
		mo:  QuantizeMultiplier(twiceMax / (float64(1<<addLeftShift) * so)),
		zp1: zeroPoint(in1T), zp2: zeroPoint(in2T), zpo: zpo,
		actMin: actMin, actMax: actMax,
	}, nil
}

func opAveragePool2D(interp *Interpreter, op model.Operator) error {
	return pool2D(interp, op, "AVERAGE_POOL_2D", false)
}

func opMaxPool2D(interp *Interpreter, op model.Operator) error {
	return pool2D(interp, op, "MAX_POOL_2D", true)
}

// pool2D implements quantized NHWC average and max pooling, following
// TFLite Micro's integer pooling kernels. Windows are clipped to the input,
// so padding never counts toward an average.
func pool2D(interp *Interpreter, op model.Operator, name string, isMax bool) error {
	if len(op.Inputs) != 1 || len(op.Outputs) != 1 {
		return fmt.Errorf("%s: bad io", name)
	}
	sg := interp.model.Subgraphs[interp.sgIdx]
	inT := sg.Tensors[op.Inputs[0]]
	outT := sg.Tensors[op.Outputs[0]]
	if inT.Type != model.TensorInt8 || outT.Type != model.TensorInt8 {
		return fmt.Errorf("%s: only int8 is supported", name)
	}
	if len(inT.Shape) != 4 || len(outT.Shape) != 4 ||
		inT.Shape[0] != outT.Shape[0] || inT.Shape[3] != outT.Shape[3] {
		return fmt.Errorf("%s: shapes %v -> %v are not NHWC with matching batch and channels", name, inT.Shape, outT.Shape)
	}
	scale := func(t model.Tensor) float64 {
		if len(t.Quant.Scale) > 0 {
			return float64(t.Quant.Scale[0])
		}
		return 0
	}
	if math.Abs(scale(inT)-scale(outT)) > 1e-6 || zeroPoint(inT) != zeroPoint(outT) {
		return fmt.Errorf("%s: input and output quantization differ", name)
	}

	opt := func(key string) int {
		if v, ok := op.Options[key]; ok {
			return v.(int)
		}
		return 0
	}
	padding := opt("padding")
	strideW, strideH := opt("stride_w"), opt("stride_h")
	filterW, filterH := opt("filter_width"), opt("filter_height")
	activation := opt("fused_activation")
	if strideW < 1 || strideH < 1 || filterW < 1 || filterH < 1 {
		return fmt.Errorf("%s: bad window %dx%d stride %dx%d", name, filterH, filterW, strideH, strideW)
	}

	batch, inH, inW, ch := inT.Shape[0], inT.Shape[1], inT.Shape[2], inT.Shape[3]
	outH, outW := outT.Shape[1], outT.Shape[2]
	in := interp.tensors[op.Inputs[0]].data
	out := interp.tensors[op.Outputs[0]].data
	if len(in) < batch*inH*inW*ch || len(out) < batch*outH*outW*ch {
		return fmt.Errorf("%s: tensor data shorter than shape", name)
	}

	padH, padW := 0, 0
	if padding == model.PaddingSame {
		padH = max(0, ((outH-1)*strideH+filterH-inH)/2)
		padW = max(0, ((outW-1)*strideW+filterW-inW)/2)
	}
	actMin, actMax := activationBounds(activation, zeroPoint(outT))

	for b := range batch {
		for oy := range outH {
			y0 := oy*strideH - padH
			fy0, fy1 := max(0, -y0), min(filterH, inH-y0)
			for ox := range outW {
				x0 := ox*strideW - padW
				fx0, fx1 := max(0, -x0), min(filterW, inW-x0)
				for c := range ch {
					acc := int32(0)
					if isMax {
						acc = -128
					}
					count := int32(0)
					for fy := fy0; fy < fy1; fy++ {
						for fx := fx0; fx < fx1; fx++ {
							v := int32(int8(in[((b*inH+y0+fy)*inW+x0+fx)*ch+c]))
							if isMax {
								acc = max(acc, v)
							} else {
								acc += v
							}
							count++
						}
					}
					if !isMax {
						if count == 0 {
							return fmt.Errorf("%s: window at (%d,%d) covers no input", name, oy, ox)
						}
						if acc > 0 {
							acc = (acc + count/2) / count
						} else {
							acc = (acc - count/2) / count
						}
					}
					acc = min(max(acc, actMin), actMax)
					out[((b*outH+oy)*outW+ox)*ch+c] = byte(int8(acc))
				}
			}
		}
	}
	return nil
}

// --- Helper functions ---

func activationBounds(activation int, outputZP int32) (int32, int32) {
	actMin := int32(-128)
	actMax := int32(127)
	switch activation {
	case model.ActRelu:
		if outputZP > actMin {
			actMin = outputZP
		}
	case model.ActRelu6:
		if outputZP > actMin {
			actMin = outputZP
		}
		// For relu6, we'd need to compute quantized 6.0, but for MWW models
		// RELU is used, not RELU6.
	}
	return actMin, actMax
}

func getBias32(data []byte, idx int) int32 {
	off := idx * 4
	if off+4 > len(data) {
		return 0
	}
	return int32(data[off]) | int32(data[off+1])<<8 |
		int32(data[off+2])<<16 | int32(data[off+3])<<24
}

// numElems is how many elements a tensor of this shape holds; a scalar
// (empty shape) holds one, matching makeTensorData.
func numElems(shape []int) int {
	if n := model.TensorSize(shape); n > 0 {
		return n
	}
	return 1
}

// zeroPoint returns a tensor's (first) zero point, 0 when it has none.
func zeroPoint(t model.Tensor) int32 {
	if len(t.Quant.ZeroPoint) > 0 {
		return int32(t.Quant.ZeroPoint[0])
	}
	return 0
}

// broadcastStrides maps output coordinates onto an input of shape in
// (NumPy rules, shapes aligned at the last dimension). The result has one
// entry per output dimension: the input's element stride along it, or 0
// where the input is broadcast.
func broadcastStrides(in, out []int) ([]int, error) {
	if len(in) > len(out) {
		return nil, fmt.Errorf("cannot broadcast %v to %v", in, out)
	}
	strides := make([]int, len(out))
	step := 1
	for i := 1; i <= len(out); i++ {
		od := out[len(out)-i]
		id := 1
		if i <= len(in) {
			id = in[len(in)-i]
		}
		switch {
		case id == od:
			strides[len(out)-i] = step
		case id == 1:
			strides[len(out)-i] = 0
		default:
			return nil, fmt.Errorf("cannot broadcast %v to %v", in, out)
		}
		step *= id
	}
	return strides, nil
}

// opSplitV splits a tensor along an axis into multiple output tensors.
// Inputs: [value, size_splits, split_dim]
//   - value: the input tensor
//   - size_splits: 1-D int32 tensor with sizes for each split
//   - split_dim: scalar int32, the dimension to split along
//
// Outputs: num_splits tensors
func opSplitV(interp *Interpreter, op model.Operator) error {
	if len(op.Inputs) < 3 || len(op.Outputs) < 2 {
		return fmt.Errorf("SPLIT_V: need 3 inputs and >=2 outputs, got %d/%d",
			len(op.Inputs), len(op.Outputs))
	}

	sg := interp.model.Subgraphs[interp.sgIdx]
	inIdx := op.Inputs[0]
	splitSizesIdx := op.Inputs[1]
	axisIdx := op.Inputs[2]

	inT := sg.Tensors[inIdx]
	inData := interp.tensors[inIdx].data
	inShape := inT.Shape
	ndim := len(inShape)

	// Read axis (scalar int32)
	axisData := interp.tensors[axisIdx].data
	axis := getInt32FromBytes(axisData, 0)
	if axis < 0 {
		axis += ndim
	}
	if axis < 0 || axis >= ndim {
		return fmt.Errorf("SPLIT_V: axis %d out of range for %d dims", axis, ndim)
	}

	// Read size_splits (int32 vector)
	numSplits := len(op.Outputs)
	splitSizesData := interp.tensors[splitSizesIdx].data
	splitSizes := make([]int, numSplits)
	inferIdx := -1
	knownSum := 0
	for i := 0; i < numSplits; i++ {
		splitSizes[i] = getInt32FromBytes(splitSizesData, i)
		if splitSizes[i] == -1 {
			inferIdx = i
		} else {
			knownSum += splitSizes[i]
		}
	}
	if inferIdx >= 0 {
		splitSizes[inferIdx] = inShape[axis] - knownSum
	}

	// Compute strides for the input tensor
	// outerSize = product of dims before axis
	// axisSize = inShape[axis]
	// innerSize = product of dims after axis
	outerSize := 1
	for d := 0; d < axis; d++ {
		outerSize *= inShape[d]
	}
	innerSize := 1
	for d := axis + 1; d < ndim; d++ {
		innerSize *= inShape[d]
	}

	// bytesPerElement
	bpe := 1
	switch inT.Type {
	case model.TensorInt32:
		bpe = 4
	case model.TensorFloat32:
		bpe = 4
	case model.TensorInt16:
		bpe = 2
	}

	// Copy data into each output
	axisOffset := 0
	for s := 0; s < numSplits; s++ {
		outIdx := op.Outputs[s]
		out := &interp.tensors[outIdx]
		sliceSize := splitSizes[s]

		pos := 0
		for o := 0; o < outerSize; o++ {
			srcStart := (o*inShape[axis] + axisOffset) * innerSize * bpe
			chunkBytes := sliceSize * innerSize * bpe
			copy(out.data[pos:pos+chunkBytes], inData[srcStart:srcStart+chunkBytes])
			pos += chunkBytes
		}
		axisOffset += sliceSize
	}

	return nil
}
