package tflite

import (
	"os"
	"runtime"
	"testing"
	"time"
)

// FuzzQuantizedModel feeds damaged copies of a quantized model (the person detector) through Parse, New
// and Invoke: whatever the file says, the answer is an error, never a panic or a hang.
func FuzzQuantizedModel(f *testing.F) {
	raw, err := os.ReadFile("../../feature/person/testdata/person_detect.tflite")
	if err != nil {
		f.Skip(err)
	}
	f.Add(raw, uint32(0), uint8(0))
	f.Add(raw, uint32(len(raw)/2), uint8(0xff))
	f.Fuzz(func(t *testing.T, model []byte, at uint32, b uint8) {
		model = append([]byte(nil), model...)
		if len(model) > 0 {
			model[int(at)%len(model)] ^= b
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			m, err := Parse(model)
			if err != nil {
				return
			}
			in, err := New(m)
			if err != nil {
				return
			}
			_ = in.Invoke()
		}()
		select {
		case <-done:
		case <-time.After(fuzzWithin):
			buf := make([]byte, 1<<16)
			t.Fatalf("still running after %v:\n%s", fuzzWithin, buf[:runtime.Stack(buf, true)])
		}
	})
}

// fuzzWithin is how long one damaged model may take, from bytes to an answer.
const fuzzWithin = 3 * time.Second

// TestDamagedQuantizedModels is a sweep of single-byte damage across the whole file, every few
// kilobytes, run as an ordinary test; the fuzz target goes further.
func TestDamagedQuantizedModels(t *testing.T) {
	raw, err := os.ReadFile("../../feature/person/testdata/person_detect.tflite")
	if err != nil {
		t.Skip(err)
	}
	for at := 0; at < len(raw); at += 4999 {
		for _, b := range []uint8{0x01, 0x80, 0xff} {
			model := append([]byte(nil), raw...)
			model[at] ^= b
			m, err := Parse(model)
			if err != nil {
				continue
			}
			in, err := New(m)
			if err != nil {
				continue
			}
			_ = in.Invoke()
		}
	}
}
