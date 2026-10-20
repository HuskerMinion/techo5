// Package audiofrontend implements the audio feature extraction pipeline
// used by microWakeWord models. It converts 16 kHz 16-bit PCM audio into
// int8 mel-spectrogram features matching TFLite Micro's microfrontend
// output.
//
// The pipeline applies: Hann windowing, FFT, mel filterbank (40 channels,
// 125–7500 Hz), spectral noise reduction, PCAN gain control, log scaling,
// and int8 quantisation.
package audiofrontend

import (
	"math"
)

// Config holds the frontend configuration parameters.
type Config struct {
	SampleRate   int
	WindowSizeMs int
	StepSizeMs   int
	NumChannels  int
	LowerFreq    float64
	UpperFreq    float64
}

// DefaultConfig returns the standard MWW frontend configuration.
func DefaultConfig(stepMs int) Config {
	return Config{
		SampleRate:   16000,
		WindowSizeMs: 30,
		StepSizeMs:   stepMs,
		NumChannels:  40,
		LowerFreq:    125.0,
		UpperFreq:    7500.0,
	}
}

// Frontend processes audio samples into features.
type Frontend struct {
	cfg        Config
	windowSize int
	stepSize   int
	fftSize    int
	hannWindow []float64
	melFilters [][]float64
	noiseEst   []float64
	firstFrame bool

	// techo5: ProcessFrame's buffers and tables (prepare)
	windowed, energy, mel []float64
	melLo, melHi          []int
	fft                   *fftPlan
}

// New creates a new Frontend with the given config.
func New(cfg Config) *Frontend {
	windowSize := cfg.SampleRate * cfg.WindowSizeMs / 1000
	stepSize := cfg.SampleRate * cfg.StepSizeMs / 1000
	fftSize := nextPow2(windowSize)
	f := &Frontend{
		cfg:        cfg,
		windowSize: windowSize,
		stepSize:   stepSize,
		fftSize:    fftSize,
		firstFrame: true,
	}
	f.hannWindow = makeHannWindow(windowSize)
	f.melFilters = makeMelFilterbank(cfg.NumChannels, fftSize, cfg.SampleRate, cfg.LowerFreq, cfg.UpperFreq)
	f.noiseEst = make([]float64, cfg.NumChannels)
	return f
}

// Reset clears the noise estimates.
func (f *Frontend) Reset() {
	f.firstFrame = true
	for i := range f.noiseEst {
		f.noiseEst[i] = 0
	}
}

// ProcessFrame extracts features from one window of samples.
// samples must have at least windowSize elements.
//
// techo5: the same arithmetic as the original (frontend_ref_test.go), in the same order, without
// its allocations: the buffers are kept, the FFT's twiddles come from a table built with the same
// recurrence, and each mel channel sums only the bins its triangle covers (the rest add +0.0).
func (f *Frontend) ProcessFrame(samples []int16) []uint16 {
	if f.windowed == nil {
		f.prepare()
	}
	// 1. Apply Hann window and convert to float64
	windowed := f.windowed
	clear(windowed)
	for i := 0; i < f.windowSize && i < len(samples); i++ {
		windowed[i] = float64(samples[i]) * f.hannWindow[i]
	}

	// 2. FFT → power spectrum
	re, im := f.fft.run(windowed)
	energy := f.energy
	for i := range energy {
		energy[i] = re[i]*re[i] + im[i]*im[i]
	}

	// 3. Mel filterbank
	melEnergy := f.mel
	for ch := range melEnergy {
		sum := 0.0
		w := f.melFilters[ch]
		for bin := f.melLo[ch]; bin < f.melHi[ch]; bin++ {
			sum += energy[bin] * w[bin]
		}
		melEnergy[ch] = math.Sqrt(math.Max(sum, 0))
	}

	// 4. Noise reduction (spectral subtraction)
	f.noiseReduce(melEnergy)

	// 5. PCAN gain control
	f.pcanGain(melEnergy)

	// 6. Log scaling
	output := make([]uint16, f.cfg.NumChannels)
	for i := 0; i < f.cfg.NumChannels; i++ {
		v := melEnergy[i]
		if v < 1 {
			v = 1
		}
		logV := math.Log(v) * (1 << 6) // scale_shift = 6
		if logV < 0 {
			logV = 0
		}
		if logV > 65535 {
			logV = 65535
		}
		output[i] = uint16(logV)
	}
	return output
}

// prepare makes ProcessFrame's buffers and tables.
func (f *Frontend) prepare() {
	numBins := f.fftSize/2 + 1
	f.windowed = make([]float64, f.fftSize)
	f.energy = make([]float64, numBins)
	f.mel = make([]float64, f.cfg.NumChannels)
	f.melLo = make([]int, f.cfg.NumChannels)
	f.melHi = make([]int, f.cfg.NumChannels)
	for ch, w := range f.melFilters {
		lo, hi := 0, 0
		for bin, v := range w {
			if v != 0 {
				if hi == 0 {
					lo = bin
				}
				hi = bin + 1
			}
		}
		f.melLo[ch], f.melHi[ch] = lo, hi
	}
	f.fft = newFFTPlan(f.fftSize)
}

// ProcessAllInt8 processes the entire audio buffer and returns int8 features.
func (f *Frontend) ProcessAllInt8(samples []int16) [][]int8 {
	f.Reset()
	var frames [][]int8
	for offset := 0; offset+f.windowSize <= len(samples); offset += f.stepSize {
		raw := f.ProcessFrame(samples[offset:])
		frame := make([]int8, f.cfg.NumChannels)
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
		frames = append(frames, frame)
	}
	return frames
}

func (f *Frontend) noiseReduce(energy []float64) {
	evenSmooth := 0.025
	oddSmooth := 0.06
	minSignal := 0.05

	for i := range energy {
		if f.firstFrame {
			f.noiseEst[i] = energy[i]
		} else {
			smooth := evenSmooth
			if energy[i] > f.noiseEst[i] {
				smooth = oddSmooth
			}
			f.noiseEst[i] = f.noiseEst[i]*(1-smooth) + energy[i]*smooth
		}
		signal := energy[i] - f.noiseEst[i]
		floor := energy[i] * minSignal
		if signal < floor {
			signal = floor
		}
		if signal < 0 {
			signal = 0
		}
		energy[i] = signal
	}
	f.firstFrame = false
}

func (f *Frontend) pcanGain(energy []float64) {
	strength := 0.95
	offset := 80.0
	for i := range energy {
		denom := math.Pow(f.noiseEst[i]+offset, strength)
		if denom > 0 {
			energy[i] = energy[i] / denom * offset
		}
	}
}
