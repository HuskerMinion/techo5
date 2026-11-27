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
	matchesTFLite(t, "add_pool", model.OpAdd, model.OpAveragePool2D, model.OpMaxPool2D)
}

// matchesTFLite runs testdata/<name>.tflite, which must hold the operations codes, on the inputs in
// testdata/<name>_tflite.bin and checks every output against TensorFlow Lite's in the same file.
func matchesTFLite(t *testing.T, name string, codes ...int) {
	t.Helper()
	m, err := model.LoadFile(filepath.Join(testRoot(), "testdata", name+".tflite"))
	if err != nil {
		t.Fatal(err)
	}
	ops := map[int]bool{}
	for _, op := range m.Subgraphs[0].Operators {
		ops[op.OpCode] = true
	}
	for _, code := range codes {
		if !ops[code] {
			t.Fatalf("the model has no %s", model.OpName(code))
		}
	}

	data, err := os.ReadFile(filepath.Join(testRoot(), "testdata", name+"_tflite.bin"))
	if err != nil {
		t.Fatal(err)
	}
	count := int(binary.LittleEndian.Uint32(data[0:]))
	inSize := int(binary.LittleEndian.Uint32(data[4:]))
	outSize := int(binary.LittleEndian.Uint32(data[8:]))
	if len(data) != 12+count*(inSize+outSize) {
		t.Fatalf("%s_tflite.bin is %d bytes, not %d", name, len(data), 12+count*(inSize+outSize))
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
