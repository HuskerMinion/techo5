package interpreter

// techo5: a whole model through ADD, AVERAGE_POOL_2D and MAX_POOL_2D, checked against TensorFlow
// Lite's reference kernels. testdata/add_pool.py makes the model and the expected outputs.

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/zserge/microwakeword/model"
)

func TestAddAndPoolModelMatchesTFLite(t *testing.T) {
	m, err := model.LoadFile(filepath.Join(testRoot(), "testdata", "add_pool.tflite"))
	if err != nil {
		t.Fatal(err)
	}
	ops := map[int]bool{}
	for _, op := range m.Subgraphs[0].Operators {
		ops[op.OpCode] = true
	}
	for _, code := range []int{model.OpAdd, model.OpAveragePool2D, model.OpMaxPool2D} {
		if !ops[code] {
			t.Fatalf("the model has no %s", model.OpName(code))
		}
	}

	data, err := os.ReadFile(filepath.Join(testRoot(), "testdata", "add_pool_tflite.bin"))
	if err != nil {
		t.Fatal(err)
	}
	count := int(binary.LittleEndian.Uint32(data[0:]))
	inSize := int(binary.LittleEndian.Uint32(data[4:]))
	outSize := int(binary.LittleEndian.Uint32(data[8:]))
	if len(data) != 12+count*(inSize+outSize) {
		t.Fatalf("add_pool_tflite.bin is %d bytes, not %d", len(data), 12+count*(inSize+outSize))
	}

	interp, err := New(m)
	if err != nil {
		t.Fatal(err)
	}
	diffs := 0
	for i := range count {
		rec := data[12+i*(inSize+outSize):]
		in := make([]int8, inSize)
		for j := range in {
			in[j] = int8(rec[j])
		}
		if err := interp.SetInput(in); err != nil {
			t.Fatal(err)
		}
		if err := interp.Invoke(); err != nil {
			t.Fatalf("input %d: %v", i, err)
		}
		got := interp.OutputUint8()
		want := rec[inSize : inSize+outSize]
		if len(got) != outSize {
			t.Fatalf("input %d: %d outputs, want %d", i, len(got), outSize)
		}
		for j := range want {
			if got[j] != want[j] {
				if diffs < 10 {
					t.Errorf("input %d, output %d: got %d, want %d", i, j, int8(got[j]), int8(want[j]))
				}
				diffs++
			}
		}
	}
	if diffs > 0 {
		t.Errorf("%d of %d outputs differ from TensorFlow Lite", diffs, count*outSize)
	}
}
