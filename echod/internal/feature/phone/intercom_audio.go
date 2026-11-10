package phone

import (
	"math"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// downsampleIntercom converts mono PCM from 48 kHz to 16 kHz.
func downsampleIntercom(samples []int16) []int16 {
	const factor = speaker.Rate / speaker.VoiceRate
	const taps = 61
	const cutoff = 0.14

	if len(samples) == 0 {
		return nil
	}

	// Build a low-pass FIR filter with a Hamming window.
	var coeff [taps]float64
	var sum float64

	for i := range coeff {
		n := float64(i - (taps-1)/2)
		var h float64

		if n == 0 {
			h = 2 * cutoff
		} else {
			h = math.Sin(2*math.Pi*cutoff*n) / (math.Pi * n)
		}

		window := 0.54 - 0.46*math.Cos(2*math.Pi*float64(i)/float64(taps-1))
		coeff[i] = h * window
		sum += coeff[i]
	}

	for i := range coeff {
		coeff[i] /= sum
	}

	out := make([]int16, (len(samples)+factor-1)/factor)
	radius := (taps - 1) / 2

	for i := range out {
		center := i * factor
		var value float64

		for j, c := range coeff {
			pos := center + j - radius
			if pos >= 0 && pos < len(samples) {
				value += float64(samples[pos]) * c
			}
		}

		value = math.Max(-32768, math.Min(32767, math.Round(value)))
		out[i] = int16(value)
	}

	return out
}

// intercomRingTone selects the custom intercom ringtone when available.
// Otherwise, it keeps the standard phone ringtone.
func intercomRingTone() []int16 {
	samples := speaker.ClipIntercomRing.OwnSamples()

	if len(samples) == 0 {
		return ringTone()
	}

	return downsampleIntercom(samples)
}
