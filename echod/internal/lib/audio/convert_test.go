package audio

import (
	"math"
	"testing"
)

// convertTone runs a stereo sine at hz and amp (of full scale) through a converter in chunks of
// chunk frames and returns the left channel.
func convertTone(from, to int, hz, amp float64, frames, chunk int) []float64 {
	c := NewConverter(from, to, 2)
	in := make([]int16, 2*frames)
	for i := range frames {
		v := int16(amp * 32767 * math.Sin(2*math.Pi*hz*float64(i)/float64(from)))
		in[2*i], in[2*i+1] = v, v
	}
	var out []int16
	for f := 0; f < frames; f += chunk {
		out = c.Run(in[2*f:2*min(f+chunk, frames)], out)
	}
	left := make([]float64, len(out)/2)
	for i := range left {
		left[i] = float64(out[2*i])
	}
	return left
}

// toneAndRest fits a sine at hz to ys (from skip on) and returns its amplitude and the RMS of what
// is left once it is taken away: everything the conversion added.
func toneAndRest(ys []float64, rate int, hz float64, skip int) (amp, rest float64) {
	var ss, sc, cc, ys1, yc float64
	for i := skip; i < len(ys); i++ {
		s, c := math.Sincos(2 * math.Pi * hz * float64(i) / float64(rate))
		ss, sc, cc = ss+s*s, sc+s*c, cc+c*c
		ys1, yc = ys1+ys[i]*s, yc+ys[i]*c
	}
	det := ss*cc - sc*sc
	a := (ys1*cc - yc*sc) / det
	b := (yc*ss - ys1*sc) / det
	var sum float64
	for i := skip; i < len(ys); i++ {
		s, c := math.Sincos(2 * math.Pi * hz * float64(i) / float64(rate))
		r := ys[i] - a*s - b*c
		sum += r * r
	}
	return math.Hypot(a, b), math.Sqrt(sum / float64(len(ys)-skip))
}

// A station's 44.1 kHz comes up to 48 with the treble at its level and nothing folded back into it:
// straight lines left a 10 kHz tone with a ghost only 21 dB under it.
func TestConverterKeepsTheTrebleClean(t *testing.T) {
	for _, hz := range []float64{1000, 5000, 10000, 15000, 18000} {
		ys := convertTone(44100, 48000, hz, 0.5, 44100, 4410)
		amp, rest := toneAndRest(ys, 48000, hz, 200)
		// The level is the 0.5 dB of headroom.
		level := 20*math.Log10(amp/(0.5*32767)) - 20*math.Log10(convertGain)
		under := 20 * math.Log10(amp/math.Sqrt2/rest)
		if math.Abs(level) > 0.1 {
			t.Errorf("%.0f Hz came out at %+.2f dB besides the headroom", hz, level)
		}
		if under < 70 {
			t.Errorf("%.0f Hz: what the conversion added is only %.1f dB under the tone", hz, under)
		}
	}
}

// A second at 44.1 kHz is exactly a second at 48, and the pieces a stream arrives in change nothing.
func TestConverterKeepsTimeWhateverTheChunks(t *testing.T) {
	whole := convertTone(44100, 48000, 440, 0.25, 44100, 44100)
	if len(whole) != 48000 {
		t.Errorf("44100 frames came out as %d, want 48000", len(whole))
	}
	c := NewConverter(44100, 48000, 2)
	in := make([]int16, 2*44100)
	for i := range 44100 {
		v := int16(8000 * math.Sin(2*math.Pi*440*float64(i)/44100))
		in[2*i], in[2*i+1] = v, v
	}
	var pieces []int16
	at := 0
	for _, n := range []int{1, 7, 441, 4410, 100, 39141} {
		pieces = c.Run(in[2*at:2*(at+n)], pieces)
		at += n
	}
	if len(pieces) != 2*len(whole) {
		t.Fatalf("in pieces %d frames, whole %d", len(pieces)/2, len(whole))
	}
	again := convertTone(44100, 48000, 440, 8000.0/32767, 44100, 44100)
	for i := range again {
		if float64(pieces[2*i]) != again[i] {
			t.Fatalf("frame %d: %d in pieces, %.0f whole", i, pieces[2*i], again[i])
		}
	}
}

// 96 kHz comes down to 48: what is above 24 kHz would fold into the treble and is gone.
func TestConverterDownStopsWhatWouldFold(t *testing.T) {
	ys := convertTone(96000, 48000, 1000, 0.5, 96000, 9600)
	if amp, _ := toneAndRest(ys, 48000, 1000, 200); math.Abs(20*math.Log10(amp/(0.5*32767*convertGain))) > 0.1 {
		t.Errorf("1 kHz came out at %.4f of what went in", amp/(0.5*32767))
	}
	// 30 kHz would land on 18 kHz.
	ys = convertTone(96000, 48000, 30000, 0.5, 96000, 9600)
	var sum float64
	for _, y := range ys[200:] {
		sum += y * y
	}
	if db := 20 * math.Log10(math.Sqrt(sum/float64(len(ys)-200))/(0.5*32767/math.Sqrt2)); db > -70 {
		t.Errorf("30 kHz folded back at %.1f dB", db)
	}
}

// Every usual rate gets a converter; one with no small ratio to the output does not.
func TestConverterRates(t *testing.T) {
	for _, from := range []int{8000, 11025, 16000, 22050, 24000, 32000, 44100, 88200, 96000, 176400, 192000} {
		if NewConverter(from, 48000, 2) == nil {
			t.Errorf("no converter from %d", from)
		}
	}
	if NewConverter(44101, 48000, 2) != nil {
		t.Error("a converter from 44101 Hz, which would need 48000 phases")
	}
}

// One second of a station: what the converter costs the device per second of music.
func BenchmarkConverterOneSecond(b *testing.B) {
	c := NewConverter(44100, 48000, 2)
	in := make([]int16, 2*4410)
	for i := range in {
		in[i] = int16(i * 37)
	}
	out := make([]int16, 0, 2*4900)
	for b.Loop() {
		for range 10 {
			out = c.Run(in, out[:0])
		}
	}
}

// Slower stations keep their treble clean too: below 44.1 kHz the images of a tone near the top
// land in the treble, and the filter is made longer to stop them.
func TestConverterSlowerStationsStayClean(t *testing.T) {
	for _, c := range []struct {
		from int
		hz   float64
	}{{16000, 7000}, {22050, 10000}, {32000, 14000}, {11025, 4500}} {
		ys := convertTone(c.from, 48000, c.hz, 0.5, c.from, c.from/10)
		amp, rest := toneAndRest(ys, 48000, c.hz, 400)
		if under := 20 * math.Log10(amp/math.Sqrt2/rest); under < 70 {
			t.Errorf("%d Hz, %.0f Hz tone: what the conversion added is only %.1f dB under it", c.from, c.hz, under)
		}
	}
}

// One channel goes through dot, two through dot2: the same filter either way.
func TestConverterMonoMatchesStereo(t *testing.T) {
	mono, stereo := NewConverter(44100, 48000, 1), NewConverter(44100, 48000, 2)
	in1, in2 := make([]int16, 4410), make([]int16, 2*4410)
	for i := range in1 {
		v := int16(12000 * math.Sin(2*math.Pi*3000*float64(i)/44100))
		in1[i], in2[2*i], in2[2*i+1] = v, v, v
	}
	a, b := mono.Run(in1, nil), stereo.Run(in2, nil)
	if 2*len(a) != len(b) {
		t.Fatalf("%d mono frames, %d stereo", len(a), len(b)/2)
	}
	for i := range a {
		if a[i] != b[2*i] || a[i] != b[2*i+1] {
			t.Fatalf("frame %d: mono %d, stereo %d %d", i, a[i], b[2*i], b[2*i+1])
		}
	}
}
