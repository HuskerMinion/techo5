package interpreter

import (
	"bytes"
	"math/rand"
	"path/filepath"
	"testing"

	"github.com/zserge/microwakeword/model"
)

// The optimized kernels (fast.go) give the same bytes as the originals (ops_ref_test.go), step after
// step, on every model in testdata: the streaming models carry state between steps, so a difference
// anywhere would grow.
func TestFastMatchesReference(t *testing.T) {
	paths, _ := filepath.Glob("../testdata/*.tflite")
	if len(paths) == 0 {
		t.Skip("no models")
	}
	ref := make(map[int]opFunc, len(opDispatch))
	for k, v := range opDispatch {
		ref[k] = v
	}
	ref[model.OpConv2D] = refConv2D
	ref[model.OpDepthwiseConv2D] = refDepthwiseConv2D
	ref[model.OpStridedSlice] = refStridedSlice

	for _, path := range paths {
		m, err := model.LoadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		fast, err := New(m)
		if err != nil {
			t.Fatal(err)
		}
		slow, err := New(m)
		if err != nil {
			t.Fatal(err)
		}
		slow.dispatch = ref
		r := rand.New(rand.NewSource(7))
		n := len(fast.tensors[m.Subgraphs[0].Inputs[0]].data)
		feat := make([]int8, n)
		for step := 0; step < 300; step++ {
			for j := range feat {
				feat[j] = int8(r.Intn(256) - 128)
			}
			fast.SetInput(feat)
			slow.SetInput(feat)
			if err := fast.Invoke(); err != nil {
				t.Fatal(err)
			}
			if err := slow.Invoke(); err != nil {
				t.Fatal(err)
			}
			for i := range fast.tensors {
				if !bytes.Equal(fast.tensors[i].data, slow.tensors[i].data) {
					t.Fatalf("%s: step %d: tensor %d differs", filepath.Base(path), step, i)
				}
			}
		}
	}
}
