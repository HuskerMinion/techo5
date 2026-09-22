//go:build !dot && !spot

package camera

import (
	"bytes"
	"image/jpeg"
	"math"
	"testing"
)

const benchQuality = 85

func packLine(vals []uint16, dst []byte) {
	for i, j := 0, 0; i+5 <= len(dst) && j+4 <= len(vals); i, j = i+5, j+4 {
		v0, v1, v2, v3 := vals[j], vals[j+1], vals[j+2], vals[j+3]
		dst[i] = byte(v0)
		dst[i+1] = byte(v0>>8) | byte(v1<<2)
		dst[i+2] = byte(v1>>6) | byte(v2<<4)
		dst[i+3] = byte(v2>>4) | byte(v3<<6)
		dst[i+4] = byte(v3 >> 2)
	}
}

// A lit room with a bright window, a dark corner and sensor noise, in the sensor's own Bayer
// order, so white balance, black point, white point and gamma all land on realistic values.
func synthRaw() []byte {
	raw := make([]byte, frameBytes)
	row := make([]uint16, sensorW)
	seed := uint32(1)
	noise := func() int {
		seed = seed*1664525 + 1013904223
		return int(seed>>16&63) - 32
	}
	for y := 0; y < sensorH; y++ {
		for x := 0; x < sensorW; x++ {
			v := 90 + 420*x/sensorW + 160*y/sensorH
			if x > sensorW*7/10 && y < sensorH*4/10 {
				v = 1023
			}
			if x < sensorW/8 && y > sensorH*3/4 {
				v = 20
			}
			switch (y&1)<<1 | x&1 {
			case 0:
				v = v * 5 / 4
			case 3:
				v = v * 3 / 4
			}
			v += noise()
			if v < 0 {
				v = 0
			}
			if v > 1023 {
				v = 1023
			}
			row[x] = uint16(v)
		}
		packLine(row, raw[y*bytesPerLine:(y+1)*bytesPerLine])
	}
	return raw
}

var benchRaw = synthRaw()

func BenchmarkMeter(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		meter(benchRaw)
	}
}

func BenchmarkRawCopy(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(frameBytes))
	for range b.N {
		dst := make([]byte, len(benchRaw))
		copy(dst, benchRaw)
	}
}

func BenchmarkConvert(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		convert(benchRaw)
	}
}

func BenchmarkTables(b *testing.B) {
	t := tone{gainR: 1.3, gainB: 0.8, white: 1500, black: 40, gamma: 1 / 1.8}
	b.ReportAllocs()
	for range b.N {
		t.tables()
	}
}

func BenchmarkTablesMiss(b *testing.B) {
	t := tone{gainR: 1.3, gainB: 0.8, white: 1500, black: 40, gamma: 1 / 1.8}
	b.ReportAllocs()
	for i := range b.N {
		t.white = 1400 + i%64
		t.tables()
	}
}

func BenchmarkStats(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		stats(benchRaw)
	}
}

func BenchmarkRender(b *testing.B) {
	t := stats(benchRaw)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		render(benchRaw, t)
	}
}

func BenchmarkEncodeStream(b *testing.B) {
	img := (&Frame{raw: benchRaw}).Image()
	var buf bytes.Buffer
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		buf.Reset()
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: benchQuality}); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(buf.Len()), "jpeg-bytes")
}

func BenchmarkStreamFrame(b *testing.B) {
	var buf bytes.Buffer
	b.ReportAllocs()
	for range b.N {
		f := &Frame{raw: benchRaw}
		buf.Reset()
		if err := jpeg.Encode(&buf, f.Image(), &jpeg.Options{Quality: benchQuality}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFull(b *testing.B) {
	f := &Frame{raw: benchRaw}
	f.Full()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		f.Full()
	}
}

func BenchmarkSnapshot(b *testing.B) {
	var buf bytes.Buffer
	b.ReportAllocs()
	for range b.N {
		f := &Frame{raw: benchRaw}
		buf.Reset()
		if err := jpeg.Encode(&buf, f.Full(), &jpeg.Options{Quality: benchQuality}); err != nil {
			b.Fatal(err)
		}
	}
}

func TestSynthRawRoundTrip(t *testing.T) {
	got := make([]uint16, sensorW)
	unpackLine(benchRaw[:bytesPerLine], got)
	dst := make([]byte, bytesPerLine)
	packLine(got, dst)
	if !bytes.Equal(dst, benchRaw[:bytesPerLine]) {
		t.Fatal("pack/unpack disagree: the synthetic frame is not what the sensor's packer would produce")
	}
	img, tn := convert(benchRaw)
	if tn.white < 32 || tn.gainR == 1 && tn.gainB == 1 {
		t.Errorf("tone %+v: the frame should push white balance off 1.0", tn)
	}
	var dark, light int
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i+1] < 16 {
			dark++
		} else if img.Pix[i+1] > 240 {
			light++
		}
	}
	if dark == 0 || light == 0 || dark+light > len(img.Pix)/8 {
		t.Errorf("converted frame is not a realistic spread: %d dark, %d light of %d", dark, light, len(img.Pix)/4)
	}
}

// The cached tables belong to the tone that asked for them.
func TestTablesCacheKeepsToneApart(t *testing.T) {
	a := tone{gainR: 1.3, gainB: 0.8, white: 1500, black: 40, gamma: 1 / 1.8}
	b := tone{gainR: 1.1, gainB: 1.4, white: 900, black: 10, gamma: 1 / 2.4}
	first := *a.tables()
	if second := *b.tables(); second == first {
		t.Fatal("two tones, one table")
	}
	if again := *a.tables(); again != first {
		t.Error("the table for a tone changed after another tone asked for one")
	}
}

// Every other cell each way measures the same picture as every cell.
func TestSampleStepDoesNotMoveTheTone(t *testing.T) {
	full, taken := sample(benchRaw, 1), stats(benchRaw)
	if math.Abs(taken.gainR-full.gainR) > 0.02 || math.Abs(taken.gainB-full.gainB) > 0.02 {
		t.Errorf("white balance %.4f/%.4f, want about %.4f/%.4f", taken.gainR, taken.gainB, full.gainR, full.gainB)
	}
	if d := taken.white - full.white; d*d > (full.white/50)*(full.white/50) {
		t.Errorf("white point %d, want about %d", taken.white, full.white)
	}
	if d := taken.black - full.black; d < -4 || d > 4 {
		t.Errorf("black point %d, want about %d", taken.black, full.black)
	}
	if math.Abs(taken.gamma-full.gamma) > 0.02 {
		t.Errorf("gamma %.3f, want about %.3f", taken.gamma, full.gamma)
	}
}
