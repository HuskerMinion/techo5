// Package microwakeword is a pure Go streaming wake-word detector
// compatible with microWakeWord v2 TFLite models
// (https://github.com/kahrendt/microWakeWord).
//
// It implements the full detection pipeline: audio feature extraction
// (mel-spectrogram), quantised TFLite model inference, and sliding-window
// probability averaging. No CGo or external C/C++ dependencies.
//
// Basic usage:
//
//	det, err := microwakeword.NewDetector(microwakeword.Config{
//	    ModelPath:         "hey_jarvis.tflite",
//	    ProbabilityCutoff: 0.85,
//	    SlidingWindowSize: 5,
//	    FeaturesStepMs:    10,
//	})
//	if err != nil { log.Fatal(err) }
//
//	for chunk := range audioSource {
//	    if det.ProcessAudio(chunk) {
//	        fmt.Println("wake word detected")
//	    }
//	}
package microwakeword

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/zserge/microwakeword/audiofrontend"
	"github.com/zserge/microwakeword/interpreter"
	"github.com/zserge/microwakeword/model"
)

// Config holds the parameters for creating a [Detector].
type Config struct {
	// ModelPath is the path to a .tflite model file.
	ModelPath string

	// ProbabilityCutoff is the sliding-window average threshold (0-1).
	// A higher value reduces false positives at the cost of sensitivity.
	ProbabilityCutoff float64

	// SlidingWindowSize is the number of recent inference results averaged
	// for the detection decision.
	SlidingWindowSize int

	// FeaturesStepMs is the audio frontend step size in milliseconds.
	// Typical values are 10 or 20. Zero defaults to 20.
	FeaturesStepMs int
}

// ConfigFromJSON reads a microWakeWord JSON config file and returns a
// [Config] with all fields populated. The JSON format follows the
// micro-wake-word-models convention.
func ConfigFromJSON(jsonPath string) (Config, error) {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return Config{}, err
	}
	var raw struct {
		MicroWakeWord struct {
			Models []struct {
				ModelPath         string  `json:"model_path,omitempty"`
				ProbabilityCutoff float64 `json:"probability_cutoff"`
				SlidingWindowSize int     `json:"sliding_window_size"`
			} `json:"models"`
			FeaturesStepMs int `json:"features_step_size,omitempty"`
		} `json:"micro_wake_word"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Config{}, err
	}
	if len(raw.MicroWakeWord.Models) == 0 {
		return Config{}, fmt.Errorf("no models in JSON")
	}
	m := raw.MicroWakeWord.Models[0]
	stepMs := raw.MicroWakeWord.FeaturesStepMs
	if stepMs == 0 {
		stepMs = 20
	}
	return Config{
		ProbabilityCutoff: m.ProbabilityCutoff,
		SlidingWindowSize: m.SlidingWindowSize,
		FeaturesStepMs:    stepMs,
	}, nil
}

// Detector is a streaming wake-word detector. It is not safe for
// concurrent use; protect with a mutex if shared across goroutines.
type Detector struct {
	cfg         Config
	fe          *audiofrontend.Frontend
	interp      *interpreter.Interpreter
	model       *model.Model
	stride      int
	featBuf     [][]int8
	probs       []uint8
	probIdx     int
	invocations int
	sampleBuf   []int16
	windowSize  int
	stepSize    int

	// OnInference, if non-nil, is called after each model inference with
	// the invocation count, the raw uint8 probability (0-255), and the
	// current sliding-window average (0-1). Useful for debugging.
	OnInference func(invocation int, rawProb uint8, slidingAvg float64)
}

// NewDetector loads a TFLite model and creates a ready-to-use detector.
func NewDetector(cfg Config) (*Detector, error) {
	if cfg.FeaturesStepMs == 0 {
		cfg.FeaturesStepMs = 20
	}

	m, err := model.LoadFile(cfg.ModelPath)
	if err != nil {
		return nil, fmt.Errorf("load model: %w", err)
	}
	interp, err := interpreter.New(m)
	if err != nil {
		return nil, fmt.Errorf("create interpreter: %w", err)
	}

	// Determine stride from the model input shape [1, stride, 40].
	sg := m.Subgraphs[0]
	stride := sg.Tensors[sg.Inputs[0]].Shape[1]

	feCfg := audiofrontend.DefaultConfig(cfg.FeaturesStepMs)
	fe := audiofrontend.New(feCfg)

	windowSize := feCfg.SampleRate * feCfg.WindowSizeMs / 1000
	stepSize := feCfg.SampleRate * feCfg.StepSizeMs / 1000

	return &Detector{
		cfg:        cfg,
		fe:         fe,
		interp:     interp,
		model:      m,
		stride:     stride,
		featBuf:    make([][]int8, 0, stride),
		probs:      make([]uint8, cfg.SlidingWindowSize),
		windowSize: windowSize,
		stepSize:   stepSize,
	}, nil
}

// ProcessAudio feeds PCM audio samples into the detector and returns true
// when the wake word is detected. Samples must be 16 kHz, 16-bit signed,
// mono. Any chunk size is accepted; the detector buffers internally.
func (d *Detector) ProcessAudio(samples []int16) bool {
	d.sampleBuf = append(d.sampleBuf, samples...)

	for len(d.sampleBuf) >= d.windowSize {
		raw := d.fe.ProcessFrame(d.sampleBuf[:d.windowSize])
		frame := make([]int8, 40)
		for i, v := range raw {
			val := (int32(v)*256 + 333) / 666
			val -= 128
			if val < -128 {
				val = -128
			}
			if val > 127 {
				val = 127
			}
			frame[i] = int8(val)
		}
		d.featBuf = append(d.featBuf, frame)
		if len(d.featBuf) >= d.stride {
			detected := d.runInference()
			d.featBuf = d.featBuf[d.stride:]
			if detected {
				return true
			}
		}
		d.sampleBuf = d.sampleBuf[d.stepSize:]
	}
	return false
}

// ProcessFeatures feeds pre-extracted int8 features (40 per frame)
// directly into the model, bypassing the audio frontend.
func (d *Detector) ProcessFeatures(frames [][]int8) bool {
	for _, frame := range frames {
		d.featBuf = append(d.featBuf, frame)
		if len(d.featBuf) >= d.stride {
			detected := d.runInference()
			d.featBuf = d.featBuf[d.stride:]
			if detected {
				return true
			}
		}
	}
	return false
}

func (d *Detector) runInference() bool {
	input := make([]int8, d.stride*40)
	for s := 0; s < d.stride; s++ {
		copy(input[s*40:], d.featBuf[s])
	}

	if err := d.interp.SetInput(input); err != nil {
		return false
	}
	if err := d.interp.Invoke(); err != nil {
		return false
	}

	out := d.interp.OutputUint8()
	prob := uint8(0)
	if len(out) > 0 {
		prob = out[0]
	}

	d.probs[d.probIdx%len(d.probs)] = prob
	d.probIdx++
	d.invocations++

	if d.OnInference != nil {
		d.OnInference(d.invocations, prob, d.SlidingAverage())
	}

	return d.isDetected()
}

func (d *Detector) isDetected() bool {
	if d.invocations < d.cfg.SlidingWindowSize {
		return false
	}
	sum := 0
	for _, p := range d.probs {
		sum += int(p)
	}
	avg := float64(sum) / float64(len(d.probs)) / 255.0
	return avg >= d.cfg.ProbabilityCutoff
}

// Reset clears all internal state so the detector can be reused on a
// new audio stream. The loaded model is kept.
func (d *Detector) Reset() {
	d.fe.Reset()
	d.interp.Reset()
	d.featBuf = d.featBuf[:0]
	d.sampleBuf = d.sampleBuf[:0]
	d.probs = make([]uint8, d.cfg.SlidingWindowSize)
	d.probIdx = 0
	d.invocations = 0
}

// SlidingAverage returns the current sliding-window average probability
// (0-1). Useful for debugging and visualisation.
func (d *Detector) SlidingAverage() float64 {
	n := d.invocations
	if n > len(d.probs) {
		n = len(d.probs)
	}
	if n == 0 {
		return 0
	}
	sum := 0
	for _, p := range d.probs {
		sum += int(p)
	}
	return float64(sum) / float64(n) / 255.0
}
