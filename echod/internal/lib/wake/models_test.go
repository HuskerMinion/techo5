package wake

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// badInputModel is a readable .tflite whose subgraph says its input is tensor 7 and then carries no
// tensors at all. Nothing in the parser makes those two agree, so the file comes back as a model and
// is indexed as though it meant something. Built by hand because it is the smallest one that shows it:
// a root table pointing at a subgraph table pointing at a one-element inputs vector, and vtables saying
// which of those fields are present.
func badInputModel() []byte {
	b := make([]byte, 60)
	put32 := func(at int, v uint32) { binary.LittleEndian.PutUint32(b[at:], v) }
	put16 := func(at int, v uint16) { binary.LittleEndian.PutUint16(b[at:], v) }

	put32(0, 20) // the root table
	copy(b[4:], "TFL3")

	// The root table's vtable: ten bytes, so three fields have slots, and only subgraphs is written.
	put16(8, 10)
	put16(10, 8)
	put16(16, 4)

	put32(20, 12) // back to the vtable at 8
	put32(24, 4)  // on to the subgraphs vector at 28

	put32(28, 1)  // one subgraph
	put32(32, 12) // at 44

	// The subgraph's vtable: eight bytes, so tensors and inputs have slots, and only inputs is written.
	put16(36, 8)
	put16(38, 8)
	put16(42, 4)

	put32(44, 8) // back to the vtable at 36
	put32(48, 4) // on to the inputs vector at 52

	put32(52, 1) // one input
	put32(56, 7) // which is tensor 7, and there are no tensors

	return b
}

// Wake word models are files that arrive from outside, and kindOf is the first thing to read one. It
// has to answer for anything it is given: a model that says nothing, a truncated download, a file that
// is not a model at all. The answer for all of them is microWakeWord, because that engine reports the
// load failure properly a moment later, and the point here is that there is an answer rather than a
// panic on whichever goroutine was listing the models.
func TestKindOfAnswersForAnyFile(t *testing.T) {
	dir := t.TempDir()

	for name, data := range map[string][]byte{
		"a bad input index": badInputModel(),
		"truncated":         badInputModel()[:9],
		"nothing":           nil,
		"text":              []byte("this is not a model, it is a note about one\n"),
		"zeroes":            make([]byte, 512),
		"ones": func() []byte {
			b := make([]byte, 512)
			for i := range b {
				b[i] = 0xFF
			}
			return b
		}(),
	} {
		path := filepath.Join(dir, name+".tflite")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: kindOf panicked: %v", name, r)
				}
			}()
			if got := kindOf(path); got != KindMicroWakeWord {
				t.Errorf("%s: kind is %v, want %v", name, got, KindMicroWakeWord)
			}
		}()
	}

	// And one that is not there at all, which is what a model deleted between the listing and the read
	// looks like.
	if got := kindOf(filepath.Join(dir, "gone.tflite")); got != KindMicroWakeWord {
		t.Errorf("a missing model is %v, want %v", got, KindMicroWakeWord)
	}
}
