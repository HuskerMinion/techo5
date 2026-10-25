package microwakeword

import (
	"testing"

	"github.com/zserge/microwakeword/interpreter"
	"github.com/zserge/microwakeword/model"
)

// techo5: a model the engine loads but cannot run says why through Err, instead of scoring zero with
// nothing to show for it. A max pool whose output is quantized unlike its input loads, and fails at
// its first step.
func TestErrSaysWhyAModelCannotRun(t *testing.T) {
	for _, broken := range []bool{false, true} {
		m, err := model.LoadFile("testdata/add_pool.tflite")
		if err != nil {
			t.Fatal(err)
		}
		sg := &m.Subgraphs[0]
		if broken {
			found := false
			for _, op := range sg.Operators {
				if op.OpCode == model.OpMaxPool2D {
					q := &sg.Tensors[op.Outputs[0]].Quant
					q.ZeroPoint = []int64{q.ZeroPoint[0] + 1}
					found = true
					break
				}
			}
			if !found {
				t.Fatal("the test model has no MAX_POOL_2D")
			}
		}
		interp, err := interpreter.New(m)
		if err != nil {
			t.Fatal(err)
		}
		stride := sg.Tensors[sg.Inputs[0]].Shape[1]
		d := &Detector{interp: interp, model: m, stride: stride, probs: make([]uint8, 1)}
		frames := make([][]int8, stride)
		for i := range frames {
			frames[i] = make([]int8, 40)
		}
		d.ProcessFeatures(frames)
		if got := d.Err(); (got != nil) != broken {
			t.Errorf("broken %v: Err() = %v", broken, got)
		}
	}
}
