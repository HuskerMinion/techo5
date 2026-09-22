package oww

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/tflite"
)

// A classifier arrives as a file from outside, so a mangled one is the shape of the problem: cut about
// in ways that still parse, and then asked to build a graph out of what it says. Every one of these has
// to come back as an error. Nothing here checks what the error says — only that Load returns one, on
// the goroutine that called it, rather than taking the daemon down from wherever inference was running.
func TestLoadRefusesAMangledClassifier(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		hurt func(b []byte)
	}{
		{"a byte every 97", func(b []byte) {
			for i := 8; i < len(b); i += 97 {
				b[i] ^= 0xFF
			}
		}},
		{"the high bit of every early word", func(b []byte) {
			for i := 8; i < min(len(b), 4096); i += 4 {
				b[i] ^= 0x80
			}
		}},
		{"the tail zeroed", func(b []byte) {
			for i := len(b) / 2; i < len(b); i++ {
				b[i] = 0
			}
		}},
		{"the tail set", func(b []byte) {
			for i := len(b) / 2; i < len(b); i++ {
				b[i] = 0xFF
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			broken := append([]byte(nil), embedModel...)
			tc.hurt(broken)

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("loading it panicked: %v", r)
				}
			}()
			if c, err := e.Load(tc.name, broken); err == nil {
				t.Errorf("loaded a mangled model as a classifier: %+v", c)
			}
		})
	}

	// Truncation on its own, at every length, since a half-written file is the likeliest damage of all.
	for n := 8; n < len(embedModel); n = n*3/2 + 1 {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("the first %d bytes panicked: %v", n, r)
				}
			}()
			if _, err := e.Load("truncated", embedModel[:n]); err == nil {
				t.Errorf("the first %d bytes loaded as a classifier", n)
			}
		}()
	}
}

// The front end's own models are built the same way, and a graph a file made up has to be refused
// there too rather than allocated. Both entry points into the interpreter are covered: the windowed
// one the mel model uses and the streaming one the embedding model uses.
func TestAMangledFrontEndModelIsRefused(t *testing.T) {
	for _, base := range [][]byte{melModel, embedModel} {
		broken := append([]byte(nil), base...)
		for i := 8; i < len(broken); i += 31 {
			broken[i] ^= 0xFF
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("a mangled front end model panicked: %v", r)
				}
			}()
			m, err := tflite.Parse(broken)
			if err != nil {
				return
			}
			if in, err := tflite.New(m); err == nil {
				in.ResizeInput(0, []int{1, embedFrames, melBins, 1})
				_ = in.Invoke()
			}
			_, _ = tflite.NewStream(m, []int{1, embedFrames, melBins, 1})
		}()
	}
}
