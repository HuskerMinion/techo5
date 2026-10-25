package wake

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/tflite"
)

// stopPath is the stop word's model, the one microWakeWord model the repository carries.
var stopPath = filepath.Join("..", "..", "feature", "detect", "assets", "stop.tflite")

// Every microWakeWord model the device carries passes the check every model is now held to before the
// engine loads it: the stop word in the repository always, and the image's wake words when
// TECHO5_MODELS names a directory of them (tools/fetch-inputs.py puts them in inputs/models). The
// openWakeWord ones among those only have to parse.
func TestBundledModelsPassTheModelCheck(t *testing.T) {
	paths := []string{stopPath}
	if dir := os.Getenv("TECHO5_MODELS"); dir != "" {
		found, err := filepath.Glob(filepath.Join(dir, "*.tflite"))
		if err != nil || len(found) == 0 {
			t.Fatalf("no models in %s (%v)", dir, err)
		}
		paths = append(paths, found...)
	}
	for _, path := range paths {
		if kindOf(path) == KindOpenWakeWord {
			// Not this engine's, and not held to its check; the hardened parser still has to read it.
			raw, _ := os.ReadFile(path)
			if _, err := tflite.Parse(raw); err != nil {
				t.Errorf("%s: %v", filepath.Base(path), err)
			}
			continue
		}
		if err := CheckMicro(path); err != nil {
			t.Errorf("%s: %v", filepath.Base(path), err)
			continue
		}
		raw, _ := os.ReadFile(path)
		m, _ := tflite.Parse(raw)
		ops, total, biggest, elems := map[string]int{}, 0, 0, 0
		for _, g := range m.Subgraphs {
			for _, o := range g.Ops {
				ops[o.Op.String()]++
			}
			for _, d := range g.Tensors {
				n := 1
				for _, x := range d.Shape {
					n *= x
					biggest = max(biggest, x)
				}
				elems = max(elems, n)
				total += max(n, 1) * elemSize(d.Type)
			}
		}
		t.Logf("%s: %d subgraphs, tensors %d bytes, largest dimension %d, largest tensor %d, operators %v",
			filepath.Base(path), len(m.Subgraphs), total, biggest, elems, ops)
	}
}

// A model with ADD, AVERAGE_POOL_2D and MAX_POOL_2D, as residual and pooled MixedNet models have, is
// taken: the engine runs all three (#190). The engine's own tests check what they give.
func TestModelCheckTakesAddAndPools(t *testing.T) {
	path := filepath.Join("..", "..", "..", "third_party", "microwakeword", "testdata", "add_pool.tflite")
	if err := CheckMicro(path); err != nil {
		t.Fatal(err)
	}
}

func stopModel(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(stopPath)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// vectorAt is where the length word of a vector in the model's root table is: the field in slot id.
func vectorAt(t *testing.T, b []byte, id int) int {
	t.Helper()
	root := binary.LittleEndian.Uint32(b)
	vt := root - binary.LittleEndian.Uint32(b[root:])
	off := binary.LittleEndian.Uint16(b[vt+4+2*uint32(id):])
	if off == 0 {
		t.Fatalf("the model has no field %d", id)
	}
	p := root + uint32(off)
	return int(p + binary.LittleEndian.Uint32(b[p:]))
}

// A file whose vector says it holds more than the file does is refused before anything is allocated
// for it. The engine's parser would make a slice of whatever the length word said: billions of
// buffers is out of memory, which is fatal, not a panic its guard can catch.
func TestModelCheckRefusesAVectorLongerThanTheFile(t *testing.T) {
	for _, field := range []int{1, 2, 4} { // operator codes, subgraphs, buffers
		b := bytes.Clone(stopModel(t))
		binary.LittleEndian.PutUint32(b[vectorAt(t, b, field):], 0x7fffffff)
		if _, err := microInput(b); err == nil {
			t.Errorf("field %d: a vector of 2^31 elements passed", field)
		}
	}
}

// The graph itself is held to what a microWakeWord model is: each limit on its own.
func TestModelCheckRefusesWhatNoWakeWordModelIs(t *testing.T) {
	tensor := func(shape ...int) *tflite.TensorDesc {
		return &tflite.TensorDesc{Type: tflite.Int8, Shape: shape}
	}
	graph := func(ops ...*tflite.OpDesc) *tflite.Subgraph {
		return &tflite.Subgraph{Tensors: []*tflite.TensorDesc{tensor(1, 3, 40), tensor(1, 1)}, Inputs: []int{0}, Outputs: []int{1}, Ops: ops}
	}
	op := func(code tflite.Op, in ...int) *tflite.OpDesc {
		return &tflite.OpDesc{Op: code, Inputs: in, Outputs: []int{1}}
	}
	many := make([]*tflite.OpDesc, microMaxOperators+1)
	for i := range many {
		many[i] = op(tflite.OpLogistic, 0)
	}
	big := []*tflite.TensorDesc{tensor(1, 3, 40), tensor(1, 1)}
	for range 5 {
		big = append(big, tensor(1024, 1024))
	}
	for _, tc := range []struct {
		name  string
		model *tflite.Model
		want  string
	}{
		{"three subgraphs", &tflite.Model{Subgraphs: []*tflite.Subgraph{graph(), graph(), graph()}}, "subgraphs"},
		{"a dimension past the limit", &tflite.Model{Subgraphs: []*tflite.Subgraph{{
			Tensors: []*tflite.TensorDesc{tensor(1, 3, 40), tensor(1, 70000)}, Inputs: []int{0}, Outputs: []int{1}}}}, "dimension"},
		{"a negative dimension", &tflite.Model{Subgraphs: []*tflite.Subgraph{{
			Tensors: []*tflite.TensorDesc{tensor(1, 3, 40), tensor(1, -1)}, Inputs: []int{0}, Outputs: []int{1}}}}, "dimension"},
		{"too many dimensions", &tflite.Model{Subgraphs: []*tflite.Subgraph{{
			Tensors: []*tflite.TensorDesc{tensor(1, 3, 40), tensor(1, 1, 1, 1, 1, 1, 1, 1, 1)}, Inputs: []int{0}, Outputs: []int{1}}}}, "dimensions"},
		{"a tensor of too many elements", &tflite.Model{Subgraphs: []*tflite.Subgraph{{
			Tensors: []*tflite.TensorDesc{tensor(1, 3, 40), tensor(4096, 4096)}, Inputs: []int{0}, Outputs: []int{1}}}}, "elements"},
		{"long dimensions multiplying past the elements", &tflite.Model{Subgraphs: []*tflite.Subgraph{{
			Tensors: []*tflite.TensorDesc{tensor(1, 3, 40), tensor(60000, 60000, 60000)}, Inputs: []int{0}, Outputs: []int{1}}}}, "elements"},
		{"tensors too big together", &tflite.Model{Subgraphs: []*tflite.Subgraph{{
			Tensors: big, Inputs: []int{0}, Outputs: []int{1}}}}, "MB"},
		{"too many operators", &tflite.Model{Subgraphs: []*tflite.Subgraph{graph(many...)}}, "operators"},
		{"an operator microWakeWord does not use", &tflite.Model{Subgraphs: []*tflite.Subgraph{graph(op(tflite.OpSoftmax, 0))}}, "SOFTMAX"},
		{"a CALL_ONCE that calls the first subgraph", &tflite.Model{Subgraphs: []*tflite.Subgraph{graph(op(tflite.OpCallOnce)), graph()}}, "calls subgraph 0"},
		{"a CALL_ONCE in the second subgraph", &tflite.Model{Subgraphs: []*tflite.Subgraph{graph(), graph(op(tflite.OpCallOnce))}}, "from subgraph 1"},
		{"an operator reading past the table", &tflite.Model{Subgraphs: []*tflite.Subgraph{graph(op(tflite.OpLogistic, 9))}}, "not in a table"},
		{"an output past the table", &tflite.Model{Subgraphs: []*tflite.Subgraph{{
			Tensors: []*tflite.TensorDesc{tensor(1, 3, 40)}, Inputs: []int{0}, Outputs: []int{4}}}}, "not in a table"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := microGraph(tc.model, 1<<20)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one about %q", err, tc.want)
			}
		})
	}
}

// A model file bigger than any microWakeWord model is refused without being read in full.
func TestCheckMicroRefusesAnOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.tflite")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxMicroFile + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := CheckMicro(path); err == nil || !strings.Contains(err.Error(), "bigger than") {
		t.Errorf("err = %v", err)
	}
}

// A newer trainer's wider window makes a tensor with one long dimension (4736 in #267); it is small all
// told, and is taken.
func TestModelCheckTakesALongDimension(t *testing.T) {
	m := &tflite.Model{Subgraphs: []*tflite.Subgraph{{
		Tensors: []*tflite.TensorDesc{{Type: tflite.Int8, Shape: []int{1, 3, 40}}, {Type: tflite.Int8, Shape: []int{1, 4736}}},
		Inputs:  []int{0}, Outputs: []int{1}}}}
	if err := microGraph(m, 1<<20); err != nil {
		t.Errorf("refused: %v", err)
	}
}
