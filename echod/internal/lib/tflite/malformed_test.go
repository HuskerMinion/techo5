package tflite

import (
	"strings"
	"testing"
)

// f32in is a float32 input tensor of the given shape, the sort every model starts with.
func f32in(name string, shape ...int) *TensorDesc {
	return &TensorDesc{Name: name, Type: Float32, Shape: shape}
}

// A model file is not a trustworthy document. Parse only proves the flatbuffer can be read, so a file
// that says something no converter would say gets all the way to New, where it used to be an index out
// of range on whichever goroutine was loading wake words. Each of these has to come back as an error.
func TestNewRefusesAModelThatParsesButMakesNoSense(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model *Model
	}{
		{"no subgraphs", &Model{}},
		{"an input that is not a tensor", &Model{Subgraphs: []*Subgraph{{
			Tensors: []*TensorDesc{f32in("x", 1, 4)},
			Inputs:  []int{7},
			Outputs: []int{0},
		}}}},
		{"an output that is not a tensor", &Model{Subgraphs: []*Subgraph{{
			Tensors: []*TensorDesc{f32in("x", 1, 4)},
			Inputs:  []int{0},
			Outputs: []int{-3},
		}}}},
		{"an operator reading past the table", &Model{Subgraphs: []*Subgraph{{
			Tensors: []*TensorDesc{f32in("x", 1, 4), f32in("y", 1, 4)},
			Inputs:  []int{0},
			Outputs: []int{1},
			Ops:     []*OpDesc{{Op: OpAdd, Inputs: []int{0, 9}, Outputs: []int{1}}},
		}}}},
		{"an operator writing past the table", &Model{Subgraphs: []*Subgraph{{
			Tensors: []*TensorDesc{f32in("x", 1, 4), f32in("y", 1, 4)},
			Inputs:  []int{0},
			Outputs: []int{1},
			Ops:     []*OpDesc{{Op: OpAdd, Inputs: []int{0, 0}, Outputs: []int{4}}},
		}}}},
		{"a negative dimension", &Model{Subgraphs: []*Subgraph{{
			Tensors: []*TensorDesc{f32in("x", 1, -4)},
			Inputs:  []int{0},
			Outputs: []int{0},
		}}}},
		{"a shape nothing could hold", &Model{Subgraphs: []*Subgraph{{
			Tensors: []*TensorDesc{f32in("x", 1<<30, 1<<30)},
			Inputs:  []int{0},
			Outputs: []int{0},
		}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, err := New(tc.model)
			if err == nil {
				t.Fatalf("built an interpreter from %s: %+v", tc.name, in)
			}
			if !strings.HasPrefix(err.Error(), "tflite: ") {
				t.Errorf("error is %q, which does not name the package", err)
			}
		})
	}
}

// The same for the streaming front end, which reads a chain of operators out of the file and so has
// its own set of things a model can fail to be.
func TestNewStreamRefusesAModelThatMakesNoSense(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model *Model
	}{
		{"no subgraphs", &Model{}},
		{"no input", &Model{Subgraphs: []*Subgraph{{
			Tensors: []*TensorDesc{f32in("x", 1, 4, 4, 1)},
			Outputs: []int{0},
		}}}},
		{"no output", &Model{Subgraphs: []*Subgraph{{
			Tensors: []*TensorDesc{f32in("x", 1, 4, 4, 1)},
			Inputs:  []int{0},
		}}}},
		{"an operator that writes nothing", &Model{Subgraphs: []*Subgraph{{
			Tensors: []*TensorDesc{f32in("x", 1, 4, 4, 1), f32in("y", 1, 4, 4, 1)},
			Inputs:  []int{0},
			Outputs: []int{1},
			Ops:     []*OpDesc{{Op: OpRelu, Inputs: []int{0}}},
		}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if s, err := NewStream(tc.model, []int{1, 4, 4, 1}); err == nil {
				t.Fatalf("built a stream from %s: %+v", tc.name, s)
			}
		})
	}
}

func TestStreamWriteTurnsAKernelPanicIntoAnError(t *testing.T) {
	// A stage NewStream would not have built: its second operand is missing, which a kernel reads
	// without asking. Write is on the wake word's goroutine, so this has to come back as an error.
	s := &Stream{stages: []stage{{
		op:       &OpDesc{Op: OpAdd, Inputs: []int{0, -1}, Outputs: []int{1}},
		consts:   []*Tensor{nil},
		out:      &Tensor{Type: Float32},
		need:     1,
		consume:  1,
		rowSize:  2,
		width:    1,
		channels: 2,
	}}}
	out, err := s.Write([]float32{1, 2})
	if err == nil || !strings.Contains(err.Error(), "malformed model") {
		t.Fatalf("Write = %v, %v; want a malformed-model error", out, err)
	}
}
