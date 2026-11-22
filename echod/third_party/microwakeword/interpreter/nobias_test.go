package interpreter

// techo5: an operator whose optional bias is left out (input -1), which TFLite allows, runs with a
// zero bias rather than indexing tensor -1.

import (
	"testing"

	"github.com/zserge/microwakeword/model"
)

// runNoBias runs one operator whose inputs are the graph input, a constant filter and no bias.
func runNoBias(t *testing.T, opCode int, opts map[string]interface{}, in, filt, out model.Tensor, w []int8, input []int8) []int8 {
	t.Helper()
	data := make([]byte, len(w))
	for i, v := range w {
		data[i] = byte(v)
	}
	filt.BufferIdx = 1
	m := &model.Model{
		Buffers: []model.Buffer{{}, {Data: data}},
		Subgraphs: []model.Subgraph{{
			Tensors: []model.Tensor{in, filt, out},
			Inputs:  []int{0},
			Outputs: []int{2},
			Operators: []model.Operator{{OpCode: opCode, Inputs: []int{0, 1, -1}, Outputs: []int{2},
				Options: opts}},
		}},
	}
	interp, err := New(m)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := interp.SetInput(input); err != nil {
		t.Fatalf("SetInput: %v", err)
	}
	if err := interp.Invoke(); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	got := make([]int8, len(interp.tensors[2].data))
	for i, v := range interp.tensors[2].data {
		got[i] = int8(v)
	}
	return got
}

func TestFullyConnectedWithoutABias(t *testing.T) {
	// out = W x, every scale 1 and zero point 0: [1 2; 3 4] [5 6] = [17 39].
	got := runNoBias(t, model.OpFullyConnected, map[string]interface{}{},
		int8Tensor([]int{1, 2}, 1, 0), int8Tensor([]int{2, 2}, 1, 0), int8Tensor([]int{1, 2}, 1, 0),
		[]int8{1, 2, 3, 4}, []int8{5, 6})
	checkClose(t, got, []int8{17, 39}, 0)
}

func TestConv2DWithoutABias(t *testing.T) {
	// A 1x1 convolution of a 1x1 picture with two channels into two: the same sum as above.
	got := runNoBias(t, model.OpConv2D, map[string]interface{}{"stride_w": 1, "stride_h": 1},
		int8Tensor([]int{1, 1, 1, 2}, 1, 0), int8Tensor([]int{2, 1, 1, 2}, 1, 0), int8Tensor([]int{1, 1, 1, 2}, 1, 0),
		[]int8{1, 2, 3, 4}, []int8{5, 6})
	checkClose(t, got, []int8{17, 39}, 0)
}
