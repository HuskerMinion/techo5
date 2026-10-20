package microwakeword_test

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"

	"github.com/zserge/microwakeword"
)

func Example() {
	det, err := microwakeword.NewDetector(microwakeword.Config{
		ModelPath:         "testdata/hey_jarvis.tflite",
		ProbabilityCutoff: 0.85,
		SlidingWindowSize: 5,
		FeaturesStepMs:    10,
	})
	if err != nil {
		log.Fatal(err)
	}

	// Load a WAV file (skip 44-byte header).
	data, err := os.ReadFile("testdata/hey_jarvis.wav")
	if err != nil {
		log.Fatal(err)
	}
	pcm := data[44:]
	samples := make([]int16, len(pcm)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(pcm[i*2:]))
	}

	// Feed 10 ms chunks (160 samples at 16 kHz).
	const chunk = 160
	for i := 0; i+chunk <= len(samples); i += chunk {
		if det.ProcessAudio(samples[i : i+chunk]) {
			fmt.Printf("detected at %.2fs\n", float64(i)/16000)
			return
		}
	}
	fmt.Println("not detected")
}
