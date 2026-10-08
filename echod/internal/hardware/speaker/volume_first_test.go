package speaker

import (
	"encoding/binary"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/asp"
)

// The curves in front of the tuning start at mute, never pass the vendor's own full volume, and the
// ratio that turns the usual gain into one of them lands exactly on it. Whether each step is louder
// than the last depends on the tuning's filters as well, so TestVolumeInFrontKeepsTheLoudness checks
// that against the real one.
func TestFirstCurves(t *testing.T) {
	for name, curve := range firstCurves {
		if curve[0] > mute {
			t.Errorf("%s: step 0 is %v dB, not muted", name, curve[0])
		}
		for s := 1; s <= VolumeSteps; s++ {
			if curve[s] > 0 {
				t.Errorf("%s: step %d is %v dB, past the vendor's own full volume", name, s, curve[s])
			}
			got := float64(gainForStep(OutputSpeaker, s) * firstRatio(&curve, OutputSpeaker, s))
			if want := math.Pow(10, curve[s]/20); math.Abs(got-want) > want*1e-4 {
				t.Errorf("%s step %d: %v, want %v", name, s, got, want)
			}
		}
		if r := firstRatio(&curve, OutputSpeaker, 0); r != 0 {
			t.Errorf("%s: step 0 lets %v through", name, r)
		}
	}
}

// musicLike is a few seconds of something like mastered pop: a kick on every beat peaking near full
// scale, a bass line, and pink noise for the rest.
func musicLike(seconds int) []float32 {
	r := rand.New(rand.NewSource(1))
	x := make([]float32, asp.Rate*seconds)
	var b0, b1, b2 float64
	for i := range x {
		tm := float64(i) / asp.Rate
		beat := math.Mod(tm, 0.5)
		kick := 0.0
		if beat < 0.2 {
			kick = 0.6 * math.Exp(-beat*18) * math.Sin(2*math.Pi*(50+100*math.Exp(-beat*35))*beat)
		}
		bass := 0.18 * math.Sin(2*math.Pi*[]float64{55, 65.4, 49, 73.4}[int(tm/2)%4]*tm)
		w := r.NormFloat64()
		b0 = 0.99765*b0 + w*0.0990460
		b1 = 0.96300*b1 + w*0.2965164
		b2 = 0.57000*b2 + w*1.0526913
		x[i] = float32(kick + bass + (b0+b1+b2+w*0.1848)*0.045)
	}
	return x
}

// levels plays src through a fresh chain at step with pre in front and post behind, and gives the
// second half's RMS and peak, in dB.
func levels(t *testing.T, tun *asp.Tuning, src []float32, step int, pre, post float32) (rms, peak float64) {
	c, err := tun.Chain(period)
	if err != nil {
		t.Fatal(err)
	}
	c.Volume(float64(step) / VolumeSteps)
	x := make([]float32, len(src)/period*period)
	for i := range x {
		x[i] = src[i] * pre
	}
	for i := 0; i < len(x); i += period {
		c.Process(x[i : i+period])
	}
	var sum, most float64
	tail := x[len(x)/2:]
	for _, v := range tail {
		v := float64(v * post)
		sum += v * v
		most = math.Max(most, math.Abs(v))
	}
	return 10 * math.Log10(sum/float64(len(tail))), 20 * math.Log10(most)
}

// vendorTuning is the unit's own tuning, which is not ours to ship, or a skip without one.
func vendorTuning(t *testing.T) (*asp.Tuning, [VolumeSteps + 1]float64) {
	dir := os.Getenv("ECHOLOCAL_VENDOR_DIR")
	if dir == "" {
		t.Skip("set ECHOLOCAL_VENDOR_DIR to a copy of /vendor/etc/audio-algorithms")
	}
	tun, err := asp.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	curve, ok := firstCurves[tun.Name()]
	if !ok {
		t.Skipf("no curve in front for the %s tuning on this build", tun.Name())
	}
	return tun, curve
}

// leveledMusic is two 30 s clips, from 20 s and 90 s in, of each 48 kHz mono float WAV in
// ECHOLOCAL_MUSIC_DIR, each leveled to -14 dBFS RMS and held to full scale: what the curves were
// worked out on.
func leveledMusic(t *testing.T) [][]float32 {
	dir := os.Getenv("ECHOLOCAL_MUSIC_DIR")
	if dir == "" {
		t.Skip("set ECHOLOCAL_MUSIC_DIR to a folder of 48 kHz mono float WAV music")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.wav"))
	var out [][]float32
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i := 12; i+8 <= len(b); {
			id, size := string(b[i:i+4]), int(binary.LittleEndian.Uint32(b[i+4:]))
			if id == "data" {
				b = b[i+8 : min(i+8+size, len(b))]
				break
			}
			i += 8 + size + size%2
		}
		x := make([]float32, len(b)/4)
		for i := range x {
			x[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
		}
		for _, from := range []int{20, 90} {
			if (from+30)*asp.Rate > len(x) {
				continue
			}
			clip := slices.Clone(x[from*asp.Rate : (from+30)*asp.Rate])
			var sum float64
			for _, v := range clip {
				sum += float64(v) * float64(v)
			}
			k := float32(math.Pow(10, (-14-10*math.Log10(sum/float64(len(clip))))/20))
			for i := range clip {
				clip[i] = max(-1, min(1, clip[i]*k))
			}
			out = append(out, clip)
		}
	}
	if len(out) == 0 {
		t.Skip("no music in ECHOLOCAL_MUSIC_DIR")
	}
	return out
}

// With the real tuning and real music, every step in front of it is about as loud as the same step was
// behind it, so nobody's dial changes, and every step is louder than the one below. The Dot's steps
// above 16 rise evenly to the same top instead (paths_dot.go), so they are held to that. Needs a copy
// of the unit's tuning and some music:
//
//	ECHOLOCAL_VENDOR_DIR=/tmp/coefs ECHOLOCAL_MUSIC_DIR=/tmp/music go test ./internal/hardware/speaker/ -run VolumeInFront -v
func TestVolumeInFrontKeepsTheLoudness(t *testing.T) {
	tun, curve := vendorTuning(t)
	songs := leveledMusic(t)
	behind := func(step int) float64 {
		var sum float64
		for _, s := range songs {
			r, _ := levels(t, tun, s, step, 1, gainForStep(OutputSpeaker, step))
			sum += r
		}
		return sum / float64(len(songs))
	}
	var from, top float64
	if tun.Name() == "dot" {
		from, top = behind(16), behind(VolumeSteps)
	}
	last := math.Inf(-1)
	for step := 1; step <= VolumeSteps; step++ {
		g := gainForStep(OutputSpeaker, step)
		was := behind(step)
		if tun.Name() == "dot" && step > 16 {
			was = from + (top-from)*float64(step-16)/float64(VolumeSteps-16)
		}
		var now float64
		for _, s := range songs {
			n, _ := levels(t, tun, s, step, g*firstRatio(&curve, OutputSpeaker, step), 1)
			now += n / float64(len(songs))
		}
		if math.Abs(now-was) > 1.5 {
			t.Errorf("step %d: %.1f dB, was %.1f dB", step, now, was)
		}
		if now <= last {
			t.Errorf("step %d: %.1f dB, no louder than the step below (%.1f dB)", step, now, last)
		}
		last = now
		t.Logf("step %2d: %.1f dB (was %.1f)", step, now, was)
	}
}

// And the music keeps its punch: the peaks stand further above the average than they did when the
// compressor was flattening every kick, wherever the compressor is not holding the top of the dial.
func TestVolumeInFrontKeepsThePunch(t *testing.T) {
	tun, curve := vendorTuning(t)
	src := musicLike(6)
	for _, step := range []int{1, 5, 10, 15, 20} {
		g := gainForStep(OutputSpeaker, step)
		oldRMS, oldPeak := levels(t, tun, src, step, 1, g)
		newRMS, newPeak := levels(t, tun, src, step, g*firstRatio(&curve, OutputSpeaker, step), 1)
		if oldCrest, newCrest := oldPeak-oldRMS, newPeak-newRMS; newCrest < oldCrest+1 {
			t.Errorf("step %d: peaks %.1f dB over the average, %.1f dB before", step, newCrest, oldCrest)
		}
		t.Logf("step %2d: peaks %.1f dB over the average (was %.1f)", step, newPeak-newRMS, oldPeak-oldRMS)
	}
}
