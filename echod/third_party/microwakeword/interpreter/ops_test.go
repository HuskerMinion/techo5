package interpreter

// techo5: tests of the ADD, AVERAGE_POOL_2D and MAX_POOL_2D options and kernels (#190).

import (
	"math"
	"strings"
	"testing"

	"github.com/zserge/microwakeword/model"
)

// runOp runs a model of a single operator once. Tensor 0 is the graph input
// (fed with input); a tensor with an entry in consts is a constant; the last
// tensor is the output, and every other tensor is an operator input.
func runOp(t *testing.T, opCode int, opts map[string]interface{}, tensors []model.Tensor, consts map[int][]int8, input []int8) ([]int8, error) {
	t.Helper()
	m := &model.Model{Buffers: []model.Buffer{{}}}
	last := len(tensors) - 1
	var inputs []int
	for i := range tensors {
		if c, ok := consts[i]; ok {
			data := make([]byte, len(c))
			for j, v := range c {
				data[j] = byte(v)
			}
			tensors[i].BufferIdx = len(m.Buffers)
			m.Buffers = append(m.Buffers, model.Buffer{Data: data})
		}
		if i != last {
			inputs = append(inputs, i)
		}
	}
	if opts == nil {
		opts = map[string]interface{}{}
	}
	m.Subgraphs = []model.Subgraph{{
		Tensors:   tensors,
		Inputs:    []int{0},
		Outputs:   []int{last},
		Operators: []model.Operator{{OpCode: opCode, Inputs: inputs, Outputs: []int{last}, Options: opts}},
	}}
	interp, err := New(m)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := interp.SetInput(input); err != nil {
		t.Fatalf("SetInput: %v", err)
	}
	if err := interp.Invoke(); err != nil {
		return nil, err
	}
	data := interp.tensors[last].data
	out := make([]int8, len(data))
	for i, v := range data {
		out[i] = int8(v)
	}
	return out, nil
}

func int8Tensor(shape []int, scale float32, zp int64) model.Tensor {
	return model.Tensor{
		Shape: shape,
		Type:  model.TensorInt8,
		Quant: model.QuantParams{Scale: []float32{scale}, ZeroPoint: []int64{zp}},
	}
}

func checkClose(t *testing.T, got, want []int8, tol int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d values, want %d", len(got), len(want))
	}
	for i := range want {
		if d := int(got[i]) - int(want[i]); d < -tol || d > tol {
			t.Errorf("[%d] = %d, want %d (±%d)", i, got[i], want[i], tol)
		}
	}
}

func TestAddBroadcastChannels(t *testing.T) {
	in1 := make([]int8, 256)
	for i := range in1 {
		in1[i] = int8(i - 128)
	}
	in2 := make([]int8, 16)
	for j := range in2 {
		in2[j] = int8(j*17 - 128)
	}
	const s1, zp1, s2, zp2 = 0.09832238, -5, 0.05569671, 4
	for _, tc := range []struct {
		name       string
		so         float32
		zpo        int64
		activation int
	}{
		{"relu", 0.05088778, -128, model.ActRelu},
		{"none", 0.15, 3, model.ActNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tensors := []model.Tensor{
				int8Tensor([]int{1, 1, 16, 16}, s1, zp1),
				int8Tensor([]int{16}, s2, zp2),
				int8Tensor([]int{1, 1, 16, 16}, tc.so, tc.zpo),
			}
			got, err := runOp(t, model.OpAdd, map[string]interface{}{"fused_activation": tc.activation},
				tensors, map[int][]int8{1: in2}, in1)
			if err != nil {
				t.Fatal(err)
			}
			actMin := -128.0
			if tc.activation == model.ActRelu {
				actMin = float64(tc.zpo)
			}
			want := make([]int8, len(in1))
			for i, q1 := range in1 {
				q2 := in2[i%16]
				real := float64(float32(s1))*float64(int(q1)-zp1) + float64(float32(s2))*float64(int(q2)-zp2)
				v := math.Round(real/float64(tc.so)) + float64(tc.zpo)
				want[i] = int8(math.Min(math.Max(v, actMin), 127))
			}
			checkClose(t, got, want, 1)
		})
	}
}

func TestAddBroadcastBothSides(t *testing.T) {
	tensors := []model.Tensor{
		int8Tensor([]int{1, 2, 1, 3}, 1, 0),
		int8Tensor([]int{1, 1, 2, 1}, 1, 0),
		int8Tensor([]int{1, 2, 2, 3}, 1, 0),
	}
	got, err := runOp(t, model.OpAdd, nil, tensors,
		map[int][]int8{1: {40, 80}}, []int8{0, 4, 8, 12, 16, 20})
	if err != nil {
		t.Fatal(err)
	}
	checkClose(t, got, []int8{40, 44, 48, 80, 84, 88, 52, 56, 60, 92, 96, 100}, 1)
}

func TestAddIncompatibleShapes(t *testing.T) {
	tensors := []model.Tensor{
		int8Tensor([]int{1, 1, 2, 3}, 1, 0),
		int8Tensor([]int{1, 1, 1, 2}, 1, 0),
		int8Tensor([]int{1, 1, 2, 3}, 1, 0),
	}
	_, err := runOp(t, model.OpAdd, nil, tensors, map[int][]int8{1: {1, 2}}, make([]int8, 6))
	if err == nil || !strings.Contains(err.Error(), "cannot broadcast") {
		t.Fatalf("err = %v, want broadcast error", err)
	}
}

func poolOpts(padding, filterH, strideH, activation int) map[string]interface{} {
	return map[string]interface{}{
		"padding": padding, "filter_height": filterH, "filter_width": 1,
		"stride_h": strideH, "stride_w": 1, "fused_activation": activation,
	}
}

func TestAveragePoolValid(t *testing.T) {
	tensors := []model.Tensor{
		int8Tensor([]int{1, 4, 1, 2}, 0.1, 0),
		int8Tensor([]int{1, 2, 1, 2}, 0.1, 0),
	}
	got, err := runOp(t, model.OpAveragePool2D, poolOpts(model.PaddingValid, 2, 2, model.ActNone),
		tensors, nil, []int8{1, 3, 2, 4, -1, 5, -2, 7})
	if err != nil {
		t.Fatal(err)
	}
	checkClose(t, got, []int8{2, 4, -2, 6}, 0)
}

func TestAveragePoolSameClipsWindow(t *testing.T) {
	tensors := []model.Tensor{
		int8Tensor([]int{1, 3, 1, 1}, 0.1, 0),
		int8Tensor([]int{1, 3, 1, 1}, 0.1, 0),
	}
	got, err := runOp(t, model.OpAveragePool2D, poolOpts(model.PaddingSame, 2, 1, model.ActNone),
		tensors, nil, []int8{10, 20, 31})
	if err != nil {
		t.Fatal(err)
	}
	checkClose(t, got, []int8{15, 26, 31}, 0)
}

func TestAveragePoolRejects(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outZP   int64
		opts    map[string]interface{}
		wantErr string
	}{
		{"quantization", 1, poolOpts(model.PaddingValid, 2, 2, model.ActNone), "quantization differ"},
		{"no options", 0, nil, "bad window"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tensors := []model.Tensor{
				int8Tensor([]int{1, 4, 1, 2}, 0.1, 0),
				int8Tensor([]int{1, 2, 1, 2}, 0.1, tc.outZP),
			}
			_, err := runOp(t, model.OpAveragePool2D, tc.opts, tensors, nil, make([]int8, 8))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestMaxPool(t *testing.T) {
	for _, tc := range []struct {
		activation int
		want       []int8
	}{
		{model.ActNone, []int8{-3, 4}},
		{model.ActRelu, []int8{0, 4}},
	} {
		tensors := []model.Tensor{
			int8Tensor([]int{1, 4, 1, 1}, 0.1, 0),
			int8Tensor([]int{1, 2, 1, 1}, 0.1, 0),
		}
		got, err := runOp(t, model.OpMaxPool2D, poolOpts(model.PaddingValid, 2, 2, tc.activation),
			tensors, nil, []int8{-5, -3, 4, 1})
		if err != nil {
			t.Fatal(err)
		}
		checkClose(t, got, tc.want, 0)
	}
}
