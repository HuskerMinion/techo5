# microwakeword

[![CI](https://github.com/zserge/microwakeword/actions/workflows/ci.yml/badge.svg)](https://github.com/zserge/microwakeword/actions/workflows/ci.yml)
[![GoDoc](https://pkg.go.dev/badge/github.com/zserge/microwakeword.svg)](https://pkg.go.dev/github.com/zserge/microwakeword)
[![Go Report Card](https://goreportcard.com/badge/github.com/zserge/microwakeword)](https://goreportcard.com/report/github.com/zserge/microwakeword)

Pure Go streaming wake-word detection using
[microWakeWord](https://github.com/kahrendt/microWakeWord) v2 TFLite models.
No CGo, no external C/C++ dependencies.

## How it works

```
PCM 16 kHz ─► Audio Frontend ─► TFLite Interpreter ─► Sliding Avg ─► detected?
              (mel features)     (quantised int8)       (probability)
```

1. **Audio frontend** — 30 ms Hann window, 10 ms step, 512-point FFT, 40-channel
   mel filterbank (125–7500 Hz), spectral noise reduction, PCAN gain control,
   log scaling, int8 quantisation.
2. **TFLite interpreter** — executes quantised int8 models with stateful resource
   variables (streaming). Supports the op subset used by MWW v2 models:
   Conv2D, DepthwiseConv2D, FullyConnected, Logistic, Quantize, Reshape,
   Concatenation, StridedSlice, SplitV, VarHandle, ReadVariable, AssignVariable,
   CallOnce; techo5: also Add (with broadcasting), AveragePool2D and MaxPool2D.
3. **Sliding-window detection** — averages the last N inference probabilities and
   compares against a cutoff threshold.

## Install

```
go get github.com/zserge/microwakeword@latest
```

## Usage

```go
package main

import (
	"fmt"
	"log"

	mww "github.com/zserge/microwakeword"
)

func main() {
	det, err := mww.NewDetector(mww.Config{
		ModelPath:         "hey_jarvis.tflite",
		ProbabilityCutoff: 0.85,
		SlidingWindowSize: 5,
		FeaturesStepMs:    10,
	})
	if err != nil {
		log.Fatal(err)
	}

	// Feed 16 kHz 16-bit signed PCM in any chunk size.
	for chunk := range audioChunks() {
		if det.ProcessAudio(chunk) {
			fmt.Println("wake word detected")
			det.Reset()
		}
	}
}
```

## Models

Download `.tflite` models from the
[micro-wake-word-models](https://github.com/kahrendt/micro-wake-word-models)
repository. Use v2 models (input shape `[1, 3, 40]`, int8).

Available wake words include: `alexa`, `hey_jarvis`, `hey_mycroft`,
`okay_nabu`, and others.

## CLI tools

### detect — offline WAV detection

```
go run ./cmd/detect -model hey_jarvis.tflite -wav test.wav
```

Prints per-frame probabilities and reports detection time.

### mic — live microphone detection

```
cd cmd/mic && go run . -model ../../testdata/hey_jarvis.tflite
```

Separate module (own `go.mod`) to isolate the
[malgo](https://github.com/gen2brain/malgo) dependency from the core
library. Listens on the default capture device and prints when the wake
word is detected.

## Testing

```
go test ./...
```

Tests require `.tflite` model files and `.wav` audio samples in `testdata/`.
Tests are skipped automatically if the files are not present.

## Benchmarks

```
go test -bench=. -benchmem
```

On Mac M1 it's 50x faster than real-time (20ms to process 1s of audio).

## Project structure

```
microwakeword.go        main API: Detector, Config, NewDetector, ProcessAudio
audiofrontend/          mel-spectrogram feature extraction (FFT, filterbank)
model/                  TFLite FlatBuffer parser
interpreter/            quantised TFLite interpreter (MWW op subset)
cmd/detect/             offline WAV detection CLI
cmd/mic/                live microphone detection CLI (separate go.mod)
testdata/               test models and audio samples
```

## License

MIT
