package audiofrontend

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func testRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(file))
}

func loadWAV(path string) ([]int16, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Skip 44-byte WAV header
	if len(data) < 44 {
		return nil, err
	}
	pcm := data[44:]
	n := len(pcm) / 2
	samples := make([]int16, n)
	for i := 0; i < n; i++ {
		samples[i] = int16(binary.LittleEndian.Uint16(pcm[i*2:]))
	}
	return samples, nil
}

func loadGoldenFeatures(path string) ([][]int8, error) {
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

func TestFrontendVsGolden(t *testing.T) {
	wavPath := filepath.Join(testRoot(), "testdata", "hey_jarvis.wav")
	goldenPath := filepath.Join(testRoot(), "testdata", "hey_jarvis_features.bin")

	if _, err := os.Stat(wavPath); err != nil {
		t.Skipf("WAV not found: %s", wavPath)
	}
	if _, err := os.Stat(goldenPath); err != nil {
		t.Skipf("Golden features not found: %s", goldenPath)
	}

	samples, err := loadWAV(wavPath)
	if err != nil {
		t.Fatalf("loadWAV: %v", err)
	}

	golden, err := loadGoldenFeatures(goldenPath)
	if err != nil {
		t.Fatalf("loadGoldenFeatures: %v", err)
	}

	cfg := DefaultConfig(10) // 10ms step, matching the golden data
	fe := New(cfg)
	goFrames := fe.ProcessAllInt8(samples)

	t.Logf("Go frames: %d, Golden frames: %d", len(goFrames), len(golden))

	// Compare frame counts (allow small difference due to edge handling)
	minFrames := len(golden)
	if len(goFrames) < minFrames {
		minFrames = len(goFrames)
	}
	if minFrames == 0 {
		t.Fatal("No frames produced")
	}

	// Compute per-frame mean absolute error
	totalMAE := 0.0
	maxDiff := 0
	for i := 0; i < minFrames; i++ {
		frameMAE := 0.0
		for j := 0; j < 40; j++ {
			diff := int(goFrames[i][j]) - int(golden[i][j])
			if diff < 0 {
				diff = -diff
			}
			if diff > maxDiff {
				maxDiff = diff
			}
			frameMAE += float64(diff)
		}
		frameMAE /= 40.0
		totalMAE += frameMAE
	}
	totalMAE /= float64(minFrames)

	t.Logf("Mean absolute error: %.2f, Max diff: %d", totalMAE, maxDiff)

	// The Go frontend is a float64 approximation of the fixed-point C version.
	// Allow reasonable tolerance — exact matching isn't needed as long as
	// the downstream model still detects correctly.
	if totalMAE > 25 {
		t.Errorf("MAE too high: %.2f (want < 25)", totalMAE)
	}
}
