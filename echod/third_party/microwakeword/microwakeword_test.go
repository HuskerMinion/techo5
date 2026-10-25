package microwakeword

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func testdataDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata")
}

func loadTestWAV(path string) ([]int16, error) {
	data, err := os.ReadFile(path)
	if err != nil {
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

func TestDetectHeyJarvis(t *testing.T) {
	testDetection(t, "hey_jarvis.tflite", "hey_jarvis.wav", 0.85)
}

func TestDetectAlexa(t *testing.T) {
	testDetection(t, "alexa.tflite", "alexa.wav", 0.85)
}

func TestDetectOkayNabu(t *testing.T) {
	t.Skip("okay_nabu.tflite model not working with synthesized sounds yet")
	testDetection(t, "okay_nabu.tflite", "okay_nabu.wav", 0.85)
}

func testDetection(t *testing.T, model, wav string, cutoff float64) {
	t.Helper()
	modelPath := filepath.Join(testdataDir(), model)
	wavPath := filepath.Join(testdataDir(), wav)

	if _, err := os.Stat(modelPath); err != nil {
		t.Skipf("model not found: %s", modelPath)
	}
	if _, err := os.Stat(wavPath); err != nil {
		t.Skipf("WAV not found: %s", wavPath)
	}

	det, err := NewDetector(Config{
		ModelPath:         modelPath,
		ProbabilityCutoff: cutoff,
		SlidingWindowSize: 5,
		FeaturesStepMs:    10,
	})
	if err != nil {
		t.Fatalf("NewDetector: %v", err)
	}

	samples, err := loadTestWAV(wavPath)
	if err != nil {
		t.Fatalf("loadTestWAV: %v", err)
	}
	t.Logf("audio: %d samples (%.2fs)", len(samples), float64(len(samples))/16000)

	const chunk = 160
	for i := 0; i+chunk <= len(samples); i += chunk {
		if det.ProcessAudio(samples[i : i+chunk]) {
			t.Logf("detected at sample %d (%.2fs), avg=%.4f",
				i, float64(i)/16000, det.SlidingAverage())
			return
		}
	}
	t.Error("wake word not detected")
}

func TestNoFalsePositive(t *testing.T) {
	testNoDetection(t, "hey_jarvis.tflite", "nowake.wav", 0.85)
}

func TestNoFalsePositiveCrossModel(t *testing.T) {
	// Each model should not trigger on the other's wake word.
	testNoDetection(t, "hey_jarvis.tflite", "alexa.wav", 0.85)
	testNoDetection(t, "alexa.tflite", "hey_jarvis.wav", 0.85)
}

func testNoDetection(t *testing.T, model, wav string, cutoff float64) {
	t.Helper()
	modelPath := filepath.Join(testdataDir(), model)
	wavPath := filepath.Join(testdataDir(), wav)

	if _, err := os.Stat(modelPath); err != nil {
		t.Skipf("model not found: %s", modelPath)
	}
	if _, err := os.Stat(wavPath); err != nil {
		t.Skipf("WAV not found: %s", wavPath)
	}

	det, err := NewDetector(Config{
		ModelPath:         modelPath,
		ProbabilityCutoff: 0.85,
		SlidingWindowSize: 5,
		FeaturesStepMs:    10,
	})
	if err != nil {
		t.Fatalf("NewDetector: %v", err)
	}

	samples, err := loadTestWAV(wavPath)
	if err != nil {
		t.Fatalf("loadTestWAV: %v", err)
	}

	const chunk = 160
	for i := 0; i+chunk <= len(samples); i += chunk {
		if det.ProcessAudio(samples[i : i+chunk]) {
			t.Errorf("false positive at sample %d (%.2fs)", i, float64(i)/16000)
			return
		}
	}
}

func BenchmarkProcessAudio(b *testing.B) {
	modelPath := filepath.Join(testdataDir(), "hey_jarvis.tflite")
	wavPath := filepath.Join(testdataDir(), "hey_jarvis.wav")

	if _, err := os.Stat(modelPath); err != nil {
		b.Skipf("model not found: %s", modelPath)
	}
	if _, err := os.Stat(wavPath); err != nil {
		b.Skipf("WAV not found: %s", wavPath)
	}

	samples, err := loadTestWAV(wavPath)
	if err != nil {
		b.Fatalf("loadTestWAV: %v", err)
	}

	det, err := NewDetector(Config{
		ModelPath:         modelPath,
		ProbabilityCutoff: 0.85,
		SlidingWindowSize: 5,
		FeaturesStepMs:    10,
	})
	if err != nil {
		b.Fatalf("NewDetector: %v", err)
	}

	const chunk = 160
	b.ResetTimer()
	b.ReportAllocs()

	for n := 0; n < b.N; n++ {
		det.Reset()
		for i := 0; i+chunk <= len(samples); i += chunk {
			if det.ProcessAudio(samples[i : i+chunk]) {
				break
			}
		}
	}
}
