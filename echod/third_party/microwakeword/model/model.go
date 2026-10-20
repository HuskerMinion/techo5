// Package model parses TFLite FlatBuffer (.tflite) model files into Go
// structures. It extracts tensors, operators, quantisation parameters
// and weight buffers needed for inference.
package model

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"

	flatbuffers "github.com/google/flatbuffers/go"
)

// BuiltinOperator codes from the TFLite schema.
const (
	OpAdd             = 0
	OpAveragePool2D   = 1
	OpConcatenation   = 2
	OpConv2D          = 3
	OpDepthwiseConv2D = 4
	OpFullyConnected  = 9
	OpLogistic        = 14
	OpMaxPool2D       = 17
	OpMul             = 18
	OpReshape         = 22
	OpMean            = 25
	OpPad             = 34
	OpStridedSlice    = 45
	OpPack            = 83
	OpSplitV          = 102
	OpQuantize        = 114
	OpCallOnce        = 129
	OpVarHandle       = 142
	OpReadVariable    = 143
	OpAssignVariable  = 144
)

// OpName returns a human-readable name for a builtin op code.
func OpName(code int) string {
	names := map[int]string{
		OpAdd: "ADD", OpAveragePool2D: "AVERAGE_POOL_2D",
		OpConcatenation: "CONCATENATION", OpConv2D: "CONV_2D",
		OpDepthwiseConv2D: "DEPTHWISE_CONV_2D", OpFullyConnected: "FULLY_CONNECTED",
		OpLogistic: "LOGISTIC", OpMaxPool2D: "MAX_POOL_2D",
		OpMul: "MUL", OpReshape: "RESHAPE", OpPad: "PAD",
		OpMean: "MEAN", OpPack: "PACK", OpStridedSlice: "STRIDED_SLICE",
		OpQuantize: "QUANTIZE", OpCallOnce: "CALL_ONCE",
		OpAssignVariable: "ASSIGN_VARIABLE", OpReadVariable: "READ_VARIABLE",
		OpVarHandle: "VAR_HANDLE", OpSplitV: "SPLITV",
	}
	if n, ok := names[code]; ok {
		return n
	}
	return fmt.Sprintf("OP_%d", code)
}

// Tensor type codes.
const (
	TensorFloat32 = 0
	TensorFloat16 = 1
	TensorInt32   = 2
	TensorUint8   = 3
	TensorInt64   = 4
	TensorString  = 5
	TensorBool    = 6
	TensorInt16   = 7
	TensorInt8    = 9
)

// Padding and activation constants.
const (
	PaddingSame  = 0
	PaddingValid = 1
	ActNone      = 0
	ActRelu      = 1
	ActRelu6     = 3
)

// QuantParams holds per-tensor or per-channel quantization parameters.
type QuantParams struct {
	Scale     []float32
	ZeroPoint []int64
}

// Tensor describes one tensor in the model graph.
type Tensor struct {
	Name       string
	Shape      []int
	Type       int
	BufferIdx  int
	IsVariable bool
	Quant      QuantParams
}

// Operator describes one operation in the model graph.
type Operator struct {
	OpCode        int
	Inputs        []int
	Outputs       []int
	Options       map[string]interface{}
	CustomOptions []byte
}

// Subgraph represents one computation subgraph.
type Subgraph struct {
	Name      string
	Tensors   []Tensor
	Operators []Operator
	Inputs    []int
	Outputs   []int
}

// Buffer holds raw model data (weights, biases, constants).
type Buffer struct {
	Data []byte
}

// Model is a parsed TFLite model.
type Model struct {
	Version   int
	Subgraphs []Subgraph
	Buffers   []Buffer
}

// LoadFile reads and parses a .tflite model from disk.
func LoadFile(path string) (*Model, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read model: %w", err)
	}
	return Parse(data)
}

// Parse parses a TFLite FlatBuffer from raw bytes.
func Parse(data []byte) (*Model, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("file too small")
	}
	rootOff := flatbuffers.GetUOffsetT(data)
	tab := &flatbuffers.Table{Bytes: data, Pos: flatbuffers.UOffsetT(rootOff)}
	m := &Model{}
	if o := flatbuffers.UOffsetT(tab.Offset(4)); o != 0 {
		m.Version = int(tab.GetUint32(o + tab.Pos))
	}
	m.Buffers = parseBuffers(tab)
	opCodes := parseOpCodes(tab)
	m.Subgraphs = parseSubgraphs(tab, opCodes, m.Buffers)
	return m, nil
}

// GetBufferData returns the raw bytes for a buffer index.
func (m *Model) GetBufferData(idx int) []byte {
	if idx < 0 || idx >= len(m.Buffers) {
		return nil
	}
	return m.Buffers[idx].Data
}

// GetInt8Weights returns buffer data as int8.
func (m *Model) GetInt8Weights(idx int) []int8 {
	data := m.GetBufferData(idx)
	if data == nil {
		return nil
	}
	out := make([]int8, len(data))
	for i, b := range data {
		out[i] = int8(b)
	}
	return out
}

// GetInt32Bias returns buffer data as int32.
func (m *Model) GetInt32Bias(idx int) []int32 {
	data := m.GetBufferData(idx)
	if data == nil {
		return nil
	}
	n := len(data) / 4
	out := make([]int32, n)
	for i := 0; i < n; i++ {
		out[i] = int32(binary.LittleEndian.Uint32(data[i*4 : i*4+4]))
	}
	return out
}

// TensorSize returns the total number of elements.
func TensorSize(shape []int) int {
	if len(shape) == 0 {
		return 0
	}
	n := 1
	for _, s := range shape {
		n *= s
	}
	return n
}

// --- FlatBuffer parsing helpers ---

func parseBuffers(root *flatbuffers.Table) []Buffer {
	o := flatbuffers.UOffsetT(root.Offset(12)) // field 4: buffers
	if o == 0 {
		return nil
	}
	vecLen := root.VectorLen(o)
	bufs := make([]Buffer, vecLen)
	for i := 0; i < vecLen; i++ {
		elemOff := root.Vector(o) + flatbuffers.UOffsetT(i)*4
		tableOff := elemOff + flatbuffers.UOffsetT(binary.LittleEndian.Uint32(root.Bytes[elemOff:]))
		bufTab := &flatbuffers.Table{Bytes: root.Bytes, Pos: tableOff}
		if dOff := flatbuffers.UOffsetT(bufTab.Offset(4)); dOff != 0 {
			dLen := bufTab.VectorLen(dOff)
			dStart := bufTab.Vector(dOff)
			if dLen > 0 {
				bufs[i].Data = make([]byte, dLen)
				copy(bufs[i].Data, root.Bytes[dStart:dStart+flatbuffers.UOffsetT(dLen)])
			}
		}
	}
	return bufs
}

func parseOpCodes(root *flatbuffers.Table) []int {
	o := flatbuffers.UOffsetT(root.Offset(6))
	if o == 0 {
		return nil
	}
	n := root.VectorLen(o)
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		elemOff := root.Vector(o) + flatbuffers.UOffsetT(i)*4
		tableOff := elemOff + flatbuffers.UOffsetT(binary.LittleEndian.Uint32(root.Bytes[elemOff:]))
		ocTab := &flatbuffers.Table{Bytes: root.Bytes, Pos: tableOff}
		depCode := 0
		if dOff := flatbuffers.UOffsetT(ocTab.Offset(4)); dOff != 0 {
			depCode = int(int8(ocTab.GetByte(dOff + ocTab.Pos)))
		}
		// Prefer builtin_code (voffset 10, int32) over the deprecated
		// int8 field which can't represent codes >= 128.
		if nOff := flatbuffers.UOffsetT(ocTab.Offset(10)); nOff != 0 {
			codes[i] = int(ocTab.GetInt32(nOff + ocTab.Pos))
		} else {
			codes[i] = depCode
		}
	}
	return codes
}

func parseSubgraphs(root *flatbuffers.Table, opCodes []int, buffers []Buffer) []Subgraph {
	o := flatbuffers.UOffsetT(root.Offset(8))
	if o == 0 {
		return nil
	}
	n := root.VectorLen(o)
	sgs := make([]Subgraph, n)
	for i := 0; i < n; i++ {
		elemOff := root.Vector(o) + flatbuffers.UOffsetT(i)*4
		tableOff := elemOff + flatbuffers.UOffsetT(binary.LittleEndian.Uint32(root.Bytes[elemOff:]))
		sgTab := &flatbuffers.Table{Bytes: root.Bytes, Pos: tableOff}
		sgs[i] = parseSubgraph(sgTab, opCodes, root.Bytes)
	}
	return sgs
}

func parseSubgraph(tab *flatbuffers.Table, opCodes []int, buf []byte) Subgraph {
	sg := Subgraph{}
	if o := flatbuffers.UOffsetT(tab.Offset(4)); o != 0 {
		n := tab.VectorLen(o)
		sg.Tensors = make([]Tensor, n)
		for i := 0; i < n; i++ {
			elemOff := tab.Vector(o) + flatbuffers.UOffsetT(i)*4
			tOff := elemOff + flatbuffers.UOffsetT(binary.LittleEndian.Uint32(buf[elemOff:]))
			tTab := &flatbuffers.Table{Bytes: buf, Pos: tOff}
			sg.Tensors[i] = parseTensor(tTab, buf)
		}
	}
	if o := flatbuffers.UOffsetT(tab.Offset(6)); o != 0 {
		n := tab.VectorLen(o)
		sg.Inputs = make([]int, n)
		start := tab.Vector(o)
		for i := 0; i < n; i++ {
			sg.Inputs[i] = int(int32(binary.LittleEndian.Uint32(buf[start+flatbuffers.UOffsetT(i)*4:])))
		}
	}
	if o := flatbuffers.UOffsetT(tab.Offset(8)); o != 0 {
		n := tab.VectorLen(o)
		sg.Outputs = make([]int, n)
		start := tab.Vector(o)
		for i := 0; i < n; i++ {
			sg.Outputs[i] = int(int32(binary.LittleEndian.Uint32(buf[start+flatbuffers.UOffsetT(i)*4:])))
		}
	}
	if o := flatbuffers.UOffsetT(tab.Offset(10)); o != 0 {
		n := tab.VectorLen(o)
		sg.Operators = make([]Operator, n)
		for i := 0; i < n; i++ {
			elemOff := tab.Vector(o) + flatbuffers.UOffsetT(i)*4
			opOff := elemOff + flatbuffers.UOffsetT(binary.LittleEndian.Uint32(buf[elemOff:]))
			opTab := &flatbuffers.Table{Bytes: buf, Pos: opOff}
			sg.Operators[i] = parseOperator(opTab, opCodes, buf)
		}
	}
	if o := flatbuffers.UOffsetT(tab.Offset(12)); o != 0 {
		sg.Name = string(tab.ByteVector(o + tab.Pos))
	}
	return sg
}

func parseTensor(tab *flatbuffers.Table, buf []byte) Tensor {
	t := Tensor{}
	if o := flatbuffers.UOffsetT(tab.Offset(4)); o != 0 {
		n := tab.VectorLen(o)
		t.Shape = make([]int, n)
		start := tab.Vector(o)
		for i := 0; i < n; i++ {
			t.Shape[i] = int(int32(binary.LittleEndian.Uint32(buf[start+flatbuffers.UOffsetT(i)*4:])))
		}
	}
	if o := flatbuffers.UOffsetT(tab.Offset(6)); o != 0 {
		t.Type = int(tab.GetByte(o + tab.Pos))
	}
	if o := flatbuffers.UOffsetT(tab.Offset(8)); o != 0 {
		t.BufferIdx = int(tab.GetUint32(o + tab.Pos))
	}
	if o := flatbuffers.UOffsetT(tab.Offset(10)); o != 0 {
		t.Name = string(tab.ByteVector(o + tab.Pos))
	}
	if o := flatbuffers.UOffsetT(tab.Offset(12)); o != 0 {
		qOff := tab.Pos + o
		qTableOff := qOff + flatbuffers.UOffsetT(binary.LittleEndian.Uint32(buf[qOff:]))
		qTab := &flatbuffers.Table{Bytes: buf, Pos: qTableOff}
		t.Quant = parseQuantParams(qTab, buf)
	}
	if o := flatbuffers.UOffsetT(tab.Offset(14)); o != 0 {
		t.IsVariable = tab.GetBool(o + tab.Pos)
	}
	return t
}

func parseQuantParams(tab *flatbuffers.Table, buf []byte) QuantParams {
	q := QuantParams{}
	// QuantizationParameters: min(4), max(6), scale(8), zero_point(10)
	if o := flatbuffers.UOffsetT(tab.Offset(8)); o != 0 {
		n := tab.VectorLen(o)
		q.Scale = make([]float32, n)
		start := tab.Vector(o)
		for i := 0; i < n; i++ {
			bits := binary.LittleEndian.Uint32(buf[start+flatbuffers.UOffsetT(i)*4:])
			q.Scale[i] = math.Float32frombits(bits)
		}
	}
	if o := flatbuffers.UOffsetT(tab.Offset(10)); o != 0 {
		n := tab.VectorLen(o)
		q.ZeroPoint = make([]int64, n)
		start := tab.Vector(o)
		for i := 0; i < n; i++ {
			q.ZeroPoint[i] = int64(binary.LittleEndian.Uint64(buf[start+flatbuffers.UOffsetT(i)*8:]))
		}
	}
	return q
}

func parseOperator(tab *flatbuffers.Table, opCodes []int, buf []byte) Operator {
	op := Operator{Options: make(map[string]interface{})}
	// opcode_index defaults to 0 when absent from vtable.
	idx := 0
	if o := flatbuffers.UOffsetT(tab.Offset(4)); o != 0 {
		idx = int(tab.GetUint32(o + tab.Pos))
	}
	if idx < len(opCodes) {
		op.OpCode = opCodes[idx]
	}
	if o := flatbuffers.UOffsetT(tab.Offset(6)); o != 0 {
		n := tab.VectorLen(o)
		op.Inputs = make([]int, n)
		start := tab.Vector(o)
		for i := 0; i < n; i++ {
			op.Inputs[i] = int(int32(binary.LittleEndian.Uint32(buf[start+flatbuffers.UOffsetT(i)*4:])))
		}
	}
	if o := flatbuffers.UOffsetT(tab.Offset(8)); o != 0 {
		n := tab.VectorLen(o)
		op.Outputs = make([]int, n)
		start := tab.Vector(o)
		for i := 0; i < n; i++ {
			op.Outputs[i] = int(int32(binary.LittleEndian.Uint32(buf[start+flatbuffers.UOffsetT(i)*4:])))
		}
	}
	optType := 0
	if o := flatbuffers.UOffsetT(tab.Offset(10)); o != 0 {
		optType = int(tab.GetByte(o + tab.Pos))
	}
	if optType != 0 {
		if o := flatbuffers.UOffsetT(tab.Offset(12)); o != 0 {
			optOff := tab.Pos + o
			optTableOff := optOff + flatbuffers.UOffsetT(binary.LittleEndian.Uint32(buf[optOff:]))
			optTab := &flatbuffers.Table{Bytes: buf, Pos: optTableOff}
			parseBuiltinOptions(op.OpCode, optType, optTab, op.Options)
		}
	}
	// Parse custom_options (voffset 14) — used by VarHandle for shared_name.
	if o := flatbuffers.UOffsetT(tab.Offset(14)); o != 0 {
		n := tab.VectorLen(o)
		if n > 0 {
			start := tab.Vector(o)
			op.CustomOptions = make([]byte, n)
			copy(op.CustomOptions, buf[start:start+flatbuffers.UOffsetT(n)])
		}
	}
	return op
}

func parseBuiltinOptions(opCode, optType int, tab *flatbuffers.Table, opts map[string]interface{}) {
	switch opCode {
	case OpConv2D:
		opts["padding"] = getI8(tab, 4)
		opts["stride_w"] = getI32(tab, 6)
		opts["stride_h"] = getI32(tab, 8)
		opts["fused_activation"] = getI8(tab, 10)
		opts["dilation_w"] = getI32d(tab, 12, 1)
		opts["dilation_h"] = getI32d(tab, 14, 1)
	case OpDepthwiseConv2D:
		opts["padding"] = getI8(tab, 4)
		opts["stride_w"] = getI32(tab, 6)
		opts["stride_h"] = getI32(tab, 8)
		opts["depth_multiplier"] = getI32(tab, 10)
		opts["fused_activation"] = getI8(tab, 12)
		opts["dilation_w"] = getI32d(tab, 14, 1)
		opts["dilation_h"] = getI32d(tab, 16, 1)
	case OpFullyConnected:
		opts["fused_activation"] = getI8(tab, 4)
	case OpReshape:
		if o := flatbuffers.UOffsetT(tab.Offset(4)); o != 0 {
			n := tab.VectorLen(o)
			shape := make([]int, n)
			start := tab.Vector(o)
			for i := 0; i < n; i++ {
				shape[i] = int(int32(binary.LittleEndian.Uint32(tab.Bytes[start+flatbuffers.UOffsetT(i)*4:])))
			}
			opts["new_shape"] = shape
		}
	case OpConcatenation:
		opts["axis"] = getI32(tab, 4)
		opts["fused_activation"] = getI8(tab, 6)
	case OpStridedSlice:
		opts["begin_mask"] = getI32(tab, 4)
		opts["end_mask"] = getI32(tab, 6)
		opts["ellipsis_mask"] = getI32(tab, 8)
		opts["new_axis_mask"] = getI32(tab, 10)
		opts["shrink_axis_mask"] = getI32(tab, 12)
	case OpCallOnce:
		opts["init_subgraph_index"] = getI32(tab, 4)
	}
}

func getI8(tab *flatbuffers.Table, voff flatbuffers.VOffsetT) int {
	if o := flatbuffers.UOffsetT(tab.Offset(voff)); o != 0 {
		return int(int8(tab.GetByte(o + tab.Pos)))
	}
	return 0
}

func getI32(tab *flatbuffers.Table, voff flatbuffers.VOffsetT) int {
	if o := flatbuffers.UOffsetT(tab.Offset(voff)); o != 0 {
		return int(tab.GetInt32(o + tab.Pos))
	}
	return 0
}

func getI32d(tab *flatbuffers.Table, voff flatbuffers.VOffsetT, def int) int {
	if o := flatbuffers.UOffsetT(tab.Offset(voff)); o != 0 {
		return int(tab.GetInt32(o + tab.Pos))
	}
	return def
}

// ExtractFlexBufferString extracts a string value for a given key from
// FlexBuffer-encoded custom options. This is used by VarHandle to get
// the shared_name. Returns "" if not found.
func ExtractFlexBufferString(data []byte, key string) string {
	if len(data) < 3 {
		return ""
	}
	// FlexBuffer: last byte is root byte width, second-to-last is root type.
	// For a map, root type byte has packed_type = (type << 2) | byte_width_bits
	// We'll scan the raw bytes for the key and extract the associated string.
	keyBytes := []byte(key)
	for i := 0; i+len(keyBytes) < len(data); i++ {
		match := true
		for j := 0; j < len(keyBytes); j++ {
			if data[i+j] != keyBytes[j] {
				match = false
				break
			}
		}
		if match && (i == 0 || data[i-1] == 0) {
			// Found key. Now find the corresponding value string.
			// In FlexBuffer maps, keys and values are stored separately.
			// The value string will appear elsewhere in the data.
			break
		}
	}
	// Fallback: extract all null-terminated strings and find shared_name's value.
	// In MWW models, the FlexBuffer contains {"container":"","shared_name":"XXX"}.
	// The strings appear in order: "container", "", "shared_name", "XXX"
	strings := extractNullTermStrings(data)
	for i, s := range strings {
		if s == key && i+1 < len(strings) {
			return strings[i+1]
		}
	}
	return ""
}

func extractNullTermStrings(data []byte) []string {
	var result []string
	start := -1
	for i, b := range data {
		if b == 0 {
			if start >= 0 {
				s := string(data[start:i])
				if isPrintable(s) {
					result = append(result, s)
				}
			}
			start = -1
		} else if start < 0 {
			start = i
		}
	}
	return result
}

func isPrintable(s string) bool {
	for _, c := range s {
		if c < 32 || c > 126 {
			return false
		}
	}
	return true
}
