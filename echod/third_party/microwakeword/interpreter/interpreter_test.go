package interpreter

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zserge/microwakeword/model"
)

func testRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(file))
}

func testModelPath() string {
	return filepath.Join(testRoot(), "testdata", "hey_jarvis.tflite")
}

func testFeaturesPath() string {
	return filepath.Join(testRoot(), "testdata", "hey_jarvis_features.bin")
}

// loadFeatures reads the golden features file.
// Format: uint32 frameCount, uint32 featureSize, then frameCount*featureSize bytes of int8.
func loadFeatures(path string) ([][]int8, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	frameCount := int(binary.LittleEndian.Uint32(data[0:4]))
	featureSize := int(binary.LittleEndian.Uint32(data[4:8]))
	frames := make([][]int8, frameCount)
	for i := 0; i < frameCount; i++ {
		frames[i] = make([]int8, featureSize)
		off := 8 + i*featureSize
		for j := 0; j < featureSize; j++ {
			frames[i][j] = int8(data[off+j])
		}
	}
	return frames, nil
}

func TestInferenceHeyJarvis(t *testing.T) {
	modelPath := testModelPath()
	featPath := testFeaturesPath()
	if _, err := os.Stat(modelPath); err != nil {
		t.Skipf("Model not found: %s", modelPath)
	}
	if _, err := os.Stat(featPath); err != nil {
		t.Skipf("Features not found: %s", featPath)
	}

	m, err := model.LoadFile(modelPath)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	interp, err := New(m)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	frames, err := loadFeatures(featPath)
	if err != nil {
		t.Fatalf("loadFeatures: %v", err)
	}

	t.Logf("Loaded %d frames of %d features", len(frames), len(frames[0]))

	// The model expects 3 frames at a time (stride=3).
	stride := 3
	featureSize := 40
	inputBuf := make([]int8, stride*featureSize)
	maxProb := float32(0)
	detectedAt := -1

	for i := 0; i+stride <= len(frames); i += stride {
		// Pack 3 frames into input buffer
		for s := 0; s < stride; s++ {
			copy(inputBuf[s*featureSize:], frames[i+s])
		}

		if err := interp.SetInput(inputBuf); err != nil {
			t.Fatalf("SetInput at frame %d: %v", i, err)
		}
		if err := interp.Invoke(); err != nil {
			t.Fatalf("Invoke at frame %d: %v", i, err)
		}

		prob := interp.OutputFloat32()
		if len(prob) > 0 && prob[0] > maxProb {
			maxProb = prob[0]
			detectedAt = i
		}
		if len(prob) > 0 && prob[0] > 0.5 {
			t.Logf("Frame %d: prob=%.4f", i, prob[0])
		}
	}

	t.Logf("Max prob=%.4f at frame %d", maxProb, detectedAt)
	if maxProb < 0.5 {
		t.Errorf("Expected high probability detection, got max %.4f", maxProb)
	}
}

func TestInferenceOkayNabu(t *testing.T) {
	modelPath := filepath.Join(testRoot(), "testdata", "okay_nabu.tflite")
	if _, err := os.Stat(modelPath); err != nil {
		t.Skipf("Model not found: %s", modelPath)
	}

	m, err := model.LoadFile(modelPath)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	t.Logf("Subgraphs: %d", len(m.Subgraphs))
	sg := m.Subgraphs[0]
	t.Logf("Main subgraph: %d tensors, %d ops, inputs=%v, outputs=%v",
		len(sg.Tensors), len(sg.Operators), sg.Inputs, sg.Outputs)
	inT := sg.Tensors[sg.Inputs[0]]
	outT := sg.Tensors[sg.Outputs[0]]
	t.Logf("Input: shape=%v type=%d", inT.Shape, inT.Type)
	t.Logf("Output: shape=%v type=%d", outT.Shape, outT.Type)

	// Log all op codes used
	opSet := make(map[int]int)
	for _, op := range sg.Operators {
		opSet[op.OpCode]++
	}
	for code, count := range opSet {
		t.Logf("Op %s (%d): %d occurrences", model.OpName(code), code, count)
	}

	interp, err := New(m)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Try a single inference with zeros
	stride := inT.Shape[1]
	featureSize := inT.Shape[2]
	input := make([]int8, stride*featureSize)
	if err := interp.SetInput(input); err != nil {
		t.Fatalf("SetInput: %v", err)
	}
	if err := interp.Invoke(); err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	out := interp.OutputUint8()
	t.Logf("First inference output: %v", out)

	// Run a few more to exercise resource variables
	for i := 0; i < 5; i++ {
		if err := interp.SetInput(input); err != nil {
			t.Fatalf("SetInput iteration %d: %v", i, err)
		}
		if err := interp.Invoke(); err != nil {
			t.Fatalf("Invoke iteration %d: %v", i, err)
		}
	}
	out = interp.OutputUint8()
	t.Logf("After 5 iterations output: %v", out)
}
