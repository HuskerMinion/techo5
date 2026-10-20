// Command detect loads a microWakeWord v2 model and a WAV file,
// runs detection frame-by-frame, and prints each inference result.
//
// Usage:
//
//	detect -model hey_jarvis.tflite -wav audio.wav [-cutoff 0.85] [-window 5] [-step 10]
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"

	mww "github.com/zserge/microwakeword"
)

func main() {
	modelPath := flag.String("model", "", "Path to .tflite v2 model")
	wavPath := flag.String("wav", "", "Path to 16kHz 16-bit mono WAV file")
	cutoff := flag.Float64("cutoff", 0.85, "Probability cutoff for detection")
	window := flag.Int("window", 5, "Sliding window size")
	stepMs := flag.Int("step", 10, "Feature step size in ms")
	flag.Parse()

	if *modelPath == "" || *wavPath == "" {
		fmt.Fprintf(os.Stderr, "Usage: detect -model <model.tflite> -wav <audio.wav>\n\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	samples, err := loadWAV(*wavPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading WAV: %v\n", err)
		os.Exit(1)
	}

	durationS := float64(len(samples)) / 16000.0
	fmt.Printf("Model: %s\n", *modelPath)
	fmt.Printf("Audio: %s (%d samples, %.2fs)\n", *wavPath, len(samples), durationS)
	fmt.Printf("Config: cutoff=%.2f window=%d step=%dms\n\n", *cutoff, *window, *stepMs)

	cfg := mww.Config{
		ModelPath:         *modelPath,
		ProbabilityCutoff: *cutoff,
		SlidingWindowSize: *window,
		FeaturesStepMs:    *stepMs,
	}

	det, err := mww.NewDetector(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating detector: %v\n", err)
		os.Exit(1)
	}

	stride := 3 // MWW v2 model stride
	det.OnInference = func(frameIdx int, rawProb uint8, slidingAvg float64) {
		prob := float64(rawProb) / 255.0
		timeS := float64(frameIdx) * float64(stride) * float64(*stepMs) / 1000.0
		bar := makeBar(prob, 40)
		fmt.Printf("frame %4d  t=%5.2fs  prob=%.4f  avg=%.4f  %s\n",
			frameIdx, timeS, prob, slidingAvg, bar)
	}

	chunkSize := 16000 * (*stepMs) / 1000
	if chunkSize < 1 {
		chunkSize = 160
	}

	detected := false
	for i := 0; i+chunkSize <= len(samples); i += chunkSize {
		if det.ProcessAudio(samples[i : i+chunkSize]) {
			timeS := float64(i) / 16000.0
			fmt.Printf("\n*** WAKE WORD DETECTED at %.2fs ***\n", timeS)
			fmt.Printf("    sliding average: %.4f (cutoff: %.4f)\n",
				det.SlidingAverage(), *cutoff)
			detected = true
			break
		}
	}

	if !detected {
		fmt.Printf("\nNo wake word detected (sliding avg: %.4f, cutoff: %.4f)\n",
			det.SlidingAverage(), *cutoff)
	}
}

func loadWAV(path string) ([]int16, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 44 {
		return nil, fmt.Errorf("file too small for WAV header")
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("not a WAV file")
	}
	pcm := data[44:]
	n := len(pcm) / 2
	samples := make([]int16, n)
	for i := 0; i < n; i++ {
		samples[i] = int16(binary.LittleEndian.Uint16(pcm[i*2:]))
	}
	return samples, nil
}

func makeBar(prob float64, width int) string {
	filled := int(prob * float64(width))
	if filled > width {
		filled = width
	}
	bar := make([]byte, width)
	for i := range bar {
		if i < filled {
			bar[i] = '#'
		} else {
			bar[i] = '.'
		}
	}
	return "|" + string(bar) + "|"
}
