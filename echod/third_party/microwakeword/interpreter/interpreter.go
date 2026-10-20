package interpreter

import (
	"fmt"

	"github.com/zserge/microwakeword/model"
)

// tensorData holds the runtime data for a single tensor.
type tensorData struct {
	data []byte
}

// makeTensorData creates a tensorData, pre-filling from the model buffer.
func makeTensorData(t model.Tensor, m *model.Model) tensorData {
	size := model.TensorSize(t.Shape)
	if size <= 0 {
		size = 1
	}
	// Determine bytes per element
	bytesPerElem := 1
	switch t.Type {
	case model.TensorInt32:
		bytesPerElem = 4
	case model.TensorInt64:
		bytesPerElem = 8
	case model.TensorFloat32:
		bytesPerElem = 4
	case model.TensorFloat16:
		bytesPerElem = 2
	case model.TensorInt16:
		bytesPerElem = 2
	}
	totalBytes := size * bytesPerElem
	td := tensorData{data: make([]byte, totalBytes)}
	// Load constant data from model buffer
	bufData := m.GetBufferData(t.BufferIdx)
	if len(bufData) > 0 {
		copy(td.data, bufData)
	}
	return td
}

// Interpreter runs forward inference on a parsed TFLite model.
// It supports the subset of TFLite ops used by microWakeWord v2 models,
// including stateful resource variables (VarHandle/ReadVariable/AssignVariable).
type Interpreter struct {
	model            *model.Model
	sgIdx            int
	tensors          []tensorData
	resources        map[string][]byte
	varHandleNames   map[int]string // sgIdx*1000+tensorIdx → shared_name
	callOnceExecuted bool

	// techo5: the operator being run, and the plans the optimized kernels (fast.go) keep per operator
	opIdx int
	plans map[int]any
	// dispatch overrides the op table (tests run the original kernels through it)
	dispatch map[int]opFunc
}

// New creates a new interpreter for the given model.
func New(m *model.Model) (*Interpreter, error) {
	if len(m.Subgraphs) == 0 {
		return nil, fmt.Errorf("model has no subgraphs")
	}
	interp := &Interpreter{
		model:          m,
		sgIdx:          0,
		resources:      make(map[string][]byte),
		varHandleNames: make(map[int]string),
	}
	interp.allocateTensors()
	return interp, nil
}

func (interp *Interpreter) allocateTensors() {
	sg := interp.model.Subgraphs[0]
	interp.tensors = make([]tensorData, len(sg.Tensors))
	for i, t := range sg.Tensors {
		interp.tensors[i] = makeTensorData(t, interp.model)
	}
}

// SetInput copies int8 features into the input tensor.
func (interp *Interpreter) SetInput(data []int8) error {
	sg := interp.model.Subgraphs[0]
	if len(sg.Inputs) == 0 {
		return fmt.Errorf("no input tensors")
	}
	inIdx := sg.Inputs[0]
	td := &interp.tensors[inIdx]
	if len(data) > len(td.data) {
		return fmt.Errorf("input too large: %d > %d", len(data), len(td.data))
	}
	for i, v := range data {
		td.data[i] = byte(v)
	}
	return nil
}

// Invoke runs one inference step.
func (interp *Interpreter) Invoke() error {
	interp.sgIdx = 0
	sg := interp.model.Subgraphs[0]
	dispatch := interp.dispatch
	if dispatch == nil {
		dispatch = opDispatch
	}
	for i, op := range sg.Operators {
		interp.opIdx = i
		fn, ok := dispatch[op.OpCode]
		if !ok {
			return fmt.Errorf("op[%d]: unsupported %s (%d)",
				i, model.OpName(op.OpCode), op.OpCode)
		}
		if err := fn(interp, op); err != nil {
			return fmt.Errorf("op[%d] %s: %w",
				i, model.OpName(op.OpCode), err)
		}
	}
	return nil
}

// OutputUint8 returns the output tensor as uint8 values.
func (interp *Interpreter) OutputUint8() []uint8 {
	sg := interp.model.Subgraphs[0]
	if len(sg.Outputs) == 0 {
		return nil
	}
	outIdx := sg.Outputs[0]
	data := interp.tensors[outIdx].data
	out := make([]uint8, len(data))
	copy(out, data)
	return out
}

// OutputFloat32 returns the dequantized output as float32.
func (interp *Interpreter) OutputFloat32() []float32 {
	sg := interp.model.Subgraphs[0]
	if len(sg.Outputs) == 0 {
		return nil
	}
	outIdx := sg.Outputs[0]
	outT := sg.Tensors[outIdx]
	data := interp.tensors[outIdx].data

	scale := float32(0)
	zp := int32(0)
	if len(outT.Quant.Scale) > 0 {
		scale = outT.Quant.Scale[0]
	}
	if len(outT.Quant.ZeroPoint) > 0 {
		zp = int32(outT.Quant.ZeroPoint[0])
	}

	out := make([]float32, len(data))
	for i, b := range data {
		if outT.Type == model.TensorUint8 {
			out[i] = float32(int32(b)-zp) * scale
		} else {
			out[i] = float32(int32(int8(b))-zp) * scale
		}
	}
	return out
}

// Reset clears all resource variables and re-allocates tensors,
// restoring the interpreter to its initial state.
func (interp *Interpreter) Reset() {
	interp.resources = make(map[string][]byte)
	interp.varHandleNames = make(map[int]string)
	interp.callOnceExecuted = false
	interp.allocateTensors()
}
