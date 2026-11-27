package interpreter

// techo5: a model averaged from three branches, as wakesmith's combined models are (#768), and
// multiplied with broadcasting, checked against TensorFlow Lite's reference kernels. testdata/mul.py
// makes the model and the expected outputs.

import (
	"testing"

	"github.com/zserge/microwakeword/model"
)

func TestMulModelMatchesTFLite(t *testing.T) {
	matchesTFLite(t, "mul", model.OpMul, model.OpAdd)
}
