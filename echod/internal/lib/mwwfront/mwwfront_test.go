package mwwfront

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"
)

// readWAV returns the 16 bit samples of a mono WAV, wherever its data chunk is (the test file has a LIST
// chunk ahead of it).
func readWAV(t testing.TB, path string) []int16 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		t.Fatalf("%s: not a WAV", path)
	}
	for at := 12; at+8 <= len(b); {
		id, size := string(b[at:at+4]), int(binary.LittleEndian.Uint32(b[at+4:]))
		at += 8
		if id == "data" {
			pcm := b[at:min(at+size, len(b))]
			s := make([]int16, len(pcm)/2)
			for i := range s {
				s[i] = int16(binary.LittleEndian.Uint16(pcm[2*i:]))
			}
			return s
		}
		at += size + size&1
	}
	t.Fatalf("%s: no data chunk", path)
	return nil
}

// readGolden reads microWakeWord's feature dump: frame count, frame size, then int8 features.
func readGolden(t testing.TB, path string) [][]int8 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	n, size := int(binary.LittleEndian.Uint32(b)), int(binary.LittleEndian.Uint32(b[4:]))
	if size != Channels || len(b) != 8+n*size {
		t.Fatalf("%s: %d frames of %d in %d bytes", path, n, size, len(b))
	}
	out := make([][]int8, n)
	for i := range out {
		out[i] = make([]int8, size)
		for j := range out[i] {
			out[i][j] = int8(b[8+i*size+j])
		}
	}
	return out
}

// features runs a front end over audio, one window per step, as the wake word engine does.
func features(f *Frontend, s []int16) [][]int8 {
	var out [][]int8
	for at := 0; at+f.WindowSamples() <= len(s); at += f.StepSamples() {
		q := make([]int8, Channels)
		Quantize(f.ProcessFrame(s[at:at+f.WindowSamples()]), q)
		out = append(out, q)
	}
	return out
}

// The features of hey_jarvis.wav have to be the reference's, every value of every frame. The reference
// is hey_jarvis_features.bin from github.com/zserge/microwakeword's testdata, microWakeWord's own
// front end (pymicro-features) over the same audio.
func TestFeaturesAreTheReferenceBitForBit(t *testing.T) {
	gold := readGolden(t, "testdata/hey_jarvis_features.bin")
	got := features(New(10), readWAV(t, "testdata/hey_jarvis.wav"))
	if len(got) < len(gold) {
		t.Fatalf("%d frames, the reference has %d", len(got), len(gold))
	}
	wrong := 0
	for i := range gold {
		for c := range gold[i] {
			if got[i][c] != gold[i][c] {
				if wrong < 10 {
					t.Errorf("frame %d channel %d: %d, the reference %d", i, c, got[i][c], gold[i][c])
				}
				wrong++
			}
		}
	}
	if wrong > 0 {
		t.Fatalf("%d of %d values differ", wrong, len(gold)*Channels)
	}
}

// The tables are made with float arithmetic at start, so they are pinned here: a compiler or platform
// that rounded one of them differently would move features by a step without failing anything else.
// The checksum is of the tables made on amd64, where Go does not fuse float operations.
func TestTablesArePinned(t *testing.T) {
	h := uint64(14695981039346656037)
	mix := func(v int64) {
		h ^= uint64(v)
		h *= 1099511628211
	}
	for _, v := range tab.coef {
		mix(int64(v))
	}
	mix(int64(tab.start))
	mix(int64(tab.end))
	for c := range tab.chanFreqStart {
		mix(int64(tab.chanFreqStart[c]))
		mix(int64(tab.chanWeightStart[c]))
		mix(int64(tab.chanWidth[c]))
	}
	for i := range tab.weights {
		mix(int64(tab.weights[i]))
		mix(int64(tab.unweights[i]))
	}
	for _, v := range tab.gainLUT {
		mix(int64(v))
	}
	for _, v := range tab.twiddles {
		mix(int64(v.r))
		mix(int64(v.i))
	}
	for _, v := range tab.superTw {
		mix(int64(v.r))
		mix(int64(v.i))
	}
	mix(int64(tab.snrShift))
	mix(int64(tab.correction))
	if got := fmt.Sprintf("%016x", h); got != pinnedTables {
		t.Fatalf("tables checksum %s, want %s", got, pinnedTables)
	}
}

const pinnedTables = "172ea6b086249b23"

// There is no reference dump at a 20 ms step, so it is held to what a step must not change: windows
// 320 samples apart, and the same audio giving the same features, from a fresh front end or one that
// was Reset after hearing it. A manifest without a step gets 20 ms, as the daemon runs it.
func TestStep20IsDeterministic(t *testing.T) {
	if got := New(0).StepSamples(); got != 320 {
		t.Fatalf("no step: %d samples a step, want 320", got)
	}
	s := readWAV(t, "testdata/hey_jarvis.wav")
	f := New(20)
	if got := f.StepSamples(); got != 320 {
		t.Fatalf("step 20: %d samples a step, want 320", got)
	}
	first := features(f, s)
	if want := (len(s)-f.WindowSamples())/f.StepSamples() + 1; len(first) != want {
		t.Fatalf("%d frames, want %d", len(first), want)
	}
	f.Reset()
	for name, got := range map[string][][]int8{"after Reset": features(f, s), "fresh": features(New(20), s)} {
		for i := range first {
			for c := range first[i] {
				if got[i][c] != first[i][c] {
					t.Fatalf("%s: frame %d channel %d: %d, first run %d", name, i, c, got[i][c], first[i][c])
				}
			}
		}
	}
}

// Silence, full scale and a step are edges the arithmetic has to survive without a panic.
func TestEdges(t *testing.T) {
	f := New(10)
	w := make([]int16, f.WindowSamples())
	for _, fill := range []func(i int) int16{
		func(int) int16 { return 0 },
		func(int) int16 { return 32767 },
		func(int) int16 { return -32768 },
		func(i int) int16 {
			if i&1 == 0 {
				return 32767
			}
			return -32768
		},
		func(i int) int16 { return int16(i * 977) },
	} {
		for i := range w {
			w[i] = fill(i)
		}
		for range 50 {
			if n := len(f.ProcessFrame(w)); n != Channels {
				t.Fatalf("%d features", n)
			}
		}
	}
}

// BenchmarkProcessFrame is one 30 ms window's features: the cost per 10 ms step.
func BenchmarkProcessFrame(b *testing.B) {
	s := readWAV(b, "testdata/hey_jarvis.wav")
	f := New(10)
	n := (len(s) - f.WindowSamples()) / f.StepSamples()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		at := (i % n) * f.StepSamples()
		f.ProcessFrame(s[at : at+f.WindowSamples()])
	}
}
