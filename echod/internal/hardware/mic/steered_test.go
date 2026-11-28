//go:build dot && cannoli

package mic

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/array"
)

// arrayFrames is speech-like noise from bearing as the measured array hears it, in 20 ms frames: a
// burst of a second, then silence, over a faint independent hiss on every microphone.
func arrayFrames(bearing float64, seconds float64, seed uint64) [][][]int16 {
	r := rand.New(rand.NewPCG(seed, 9))
	n := int(seconds * Rate)
	src := make([]float64, n+64)
	for i := range src {
		src[i] = r.NormFloat64() * 3000
	}
	var frames [][][]int16
	frame := make([][]int16, Mics)
	for i := range n {
		on := (i/Rate)%2 == 0
		for m := range Mics {
			// A fraction of a sample by linear interpolation is enough here: the band that decides is
			// low against 16 kHz.
			at := float64(i+32) - arrayGeometry.Delay(m, bearing)*Rate
			lo := int(at)
			f := at - float64(lo)
			v := r.NormFloat64() * 20
			if on {
				v += src[lo]*(1-f) + src[lo+1]*f
			}
			frame[m] = append(frame[m], int16(v))
		}
		if len(frame[0]) == 320 {
			frames = append(frames, frame)
			frame = make([][]int16, Mics)
		}
	}
	return frames
}

func TestFinderFollowsTheTalker(t *testing.T) {
	for _, deg := range []float64{0, 70, 150, 222, 300} {
		f := newFinder()
		for _, fr := range arrayFrames(array.Radians(deg), 3, uint64(deg)+1) {
			f.look(fr, false)
		}
		if f.bearing < 0 {
			t.Fatalf("source at %v°: no bearing", deg)
		}
		if d := math.Abs(math.Remainder(array.Degrees(f.bearing)-deg, 360)); d > 10 {
			t.Errorf("source at %v°: facing %.0f°", deg, array.Degrees(f.bearing))
		}
	}
}

func TestFinderIgnoresPlayback(t *testing.T) {
	f := newFinder()
	for _, fr := range arrayFrames(1, 2, 3) {
		f.look(fr, true)
	}
	if f.bearing >= 0 {
		t.Errorf("found %.0f° while the speaker was playing", array.Degrees(f.bearing))
	}
}

func TestSteeredTurnsToTheTalker(t *testing.T) {
	s := newSteered()
	for _, deg := range []float64{90, 240} {
		for _, fr := range arrayFrames(array.Radians(deg), 3, 5) {
			if out := s.Mix(fr); len(out) != len(fr[0]) {
				t.Fatalf("mixed %d samples from %d", len(out), len(fr[0]))
			}
		}
		if want := int(deg / 30); s.Beam() != want {
			t.Errorf("talker at %v°: listening on beam %d, want %d", deg, s.Beam(), want)
		}
	}
}

// The calibration takes themselves, through the daemon's own finder:
//
//	ECHOLOCAL_CAL_DIR=/path/to/takes go test -tags "dot cannoli" ./internal/hardware/mic -run CalTakes -v
//
// Each is seg-<n>.s32 as cmd/beamcal reads it, 96 kHz, six slots, the microphones first.
func TestFinderOnCalTakes(t *testing.T) {
	dir := os.Getenv("ECHOLOCAL_CAL_DIR")
	if dir == "" {
		t.Skip("set ECHOLOCAL_CAL_DIR to a directory of seg-<n>.s32 takes")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "seg-*.s32"))
	re := regexp.MustCompile(`seg-(\d+)`)
	for _, path := range files {
		seg, _ := strconv.Atoi(re.FindStringSubmatch(filepath.Base(path))[1])
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		mics := decimateTake(raw, 6, 6)
		f := newFinder()
		// The take is the noise from its first sample, which a floor learned from it would call the
		// room: start from next to nothing instead, so all of it counts.
		f.quiet.floor = 1
		for i := 0; i+320 <= len(mics[0]); i += 320 {
			frame := make([][]int16, Mics)
			for m := range frame {
				frame[m] = mics[m][i : i+320]
			}
			f.look(frame, false)
		}
		got := array.Degrees(f.bearing)
		t.Logf("seg %2d at %3d°: facing %5.1f° (%+.0f°)", seg, seg*30, got,
			math.Remainder(got-float64(seg*30), 360))
	}
}

// decimateTake takes a native take to Rate with a windowed sinc low pass, as 16-bit samples at the
// daemon's scale.
func decimateTake(raw []byte, channels, factor int) [][]int16 {
	const taps = 255
	cut := 7000.0 / float64(Rate*factor)
	h := make([]float64, taps)
	for i := range h {
		x := float64(i - taps/2)
		v := 2 * cut
		if x != 0 {
			v = math.Sin(2*math.Pi*cut*x) / (math.Pi * x)
		}
		h[i] = v * (0.42 - 0.5*math.Cos(2*math.Pi*float64(i)/(taps-1)) + 0.08*math.Cos(4*math.Pi*float64(i)/(taps-1)))
	}
	frames := len(raw) / (4 * channels)
	out := make([][]int16, Mics)
	for m := range Mics {
		x := make([]float64, frames)
		for i := range x {
			x[i] = float64(int32(binary.LittleEndian.Uint32(raw[(i*channels+m)*4:]))) / (1 << 16)
		}
		for i := 0; i+taps <= len(x); i += factor {
			var s float64
			for j, c := range h {
				s += x[i+j] * c
			}
			out[m] = append(out[m], int16(max(-32768, min(32767, s))))
		}
	}
	return out
}

// The speech takes against each tuning: how much of the time, once the talker has started, the ring
// would point within 30 degrees of them.
//
//	ECHOLOCAL_SPEECH_DIR=/path/to/takes go test -tags "dot cannoli" ./internal/hardware/mic -run SpeechTakes -v
func TestFinderOnSpeechTakes(t *testing.T) {
	dir := os.Getenv("ECHOLOCAL_SPEECH_DIR")
	if dir == "" {
		t.Skip("set ECHOLOCAL_SPEECH_DIR to a directory of seg-<n>.s32 speech takes")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "seg-*.s32"))
	re := regexp.MustCompile(`seg-(\d+)`)
	type take struct {
		seg  int
		mics [][]int16
	}
	var takes []take
	for _, path := range files {
		seg, _ := strconv.Atoi(re.FindStringSubmatch(filepath.Base(path))[1])
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		takes = append(takes, take{seg, decimateTake(raw, 6, 6)})
	}
	tunings := map[string]finderTuning{
		"default":           defaultTuning,
		"slow":              {decay: 0.95, conf: 0.06},
		"onset":             {decay: 0.9, conf: 0.06, onset: true},
		"vote":              {decay: 0.9, conf: 0.06, vote: 0.99},
		"onset+vote":        {decay: 0.9, conf: 0.06, onset: true, vote: 0.99},
		"onset+vote slow":   {decay: 0.8, conf: 0.06, onset: true, vote: 0.995},
		"onset+vote strict": {decay: 0.9, conf: 0.12, onset: true, vote: 0.99},
		"onset+vote fast":   {decay: 0.7, conf: 0.06, onset: true, vote: 0.98},
	}
	for name, tu := range tunings {
		line := ""
		var total float64
		for _, tk := range takes {
			f := newFinderTuned(tu)
			hit, n := 0, 0
			for i := 0; i+320 <= len(tk.mics[0]); i += 320 {
				frame := make([][]int16, Mics)
				for m := range frame {
					frame[m] = tk.mics[m][i : i+320]
				}
				f.look(frame, false)
				if i < Rate || f.bearing < 0 {
					continue
				}
				n++
				if math.Abs(math.Remainder(array.Degrees(f.bearing)-float64(tk.seg*30), 360)) <= 30 {
					hit++
				}
			}
			share := float64(hit) / float64(max(n, 1))
			total += share
			line += fmt.Sprintf(" seg%d %3.0f%%", tk.seg, share*100)
		}
		// How long the ring takes to turn when the talker changes: every take after every other, the
		// time from the change until it points within 30 degrees of the new one.
		var turns []float64
		for _, a := range takes {
			for _, b := range takes {
				if a.seg == b.seg {
					continue
				}
				f := newFinderTuned(tu)
				for _, tk := range []take{a, b} {
					for i := 0; i+320 <= len(tk.mics[0]); i += 320 {
						frame := make([][]int16, Mics)
						for m := range frame {
							frame[m] = tk.mics[m][i : i+320]
						}
						f.look(frame, false)
						if tk.seg == b.seg && f.bearing >= 0 &&
							math.Abs(math.Remainder(array.Degrees(f.bearing)-float64(b.seg*30), 360)) <= 30 {
							turns = append(turns, float64(i)/Rate)
							break
						}
					}
				}
			}
		}
		sort.Float64s(turns)
		median, worst := 0.0, 0.0
		if len(turns) > 0 {
			median, worst = turns[len(turns)/2], turns[len(turns)-1]
		}
		t.Logf("%-18s mean %3.0f%% turn median %.1fs worst %.1fs (%d/30) |%s", name,
			total/float64(len(takes))*100, median, worst, len(turns), line)
	}
}

// playbackScene is the measured array over its own speaker: noise played through a point a few
// centimetres under the microphones, as the loopback carries it and as each microphone hears it, and a
// talker from bearing at talkerDB against the speaker as the microphones hear it (none at all at
// -inf). The talker speaks a second, then pauses a second, if bursts.
type playbackScene struct {
	mics [][][]int16 // per 20 ms frame, per microphone
	refs [][]int16   // per frame, the loopback
}

func newPlaybackScene(bearing, talkerDB float64, bursts bool, seconds float64, seed uint64) playbackScene {
	r := rand.New(rand.NewPCG(seed, 17))
	n := int(seconds * Rate)
	play := make([]float64, n+64)
	talk := make([]float64, n+64)
	for i := range play {
		play[i] = r.NormFloat64() * 6000
		talk[i] = r.NormFloat64()
	}
	// The speaker sits under the middle of the array, a little off it: each microphone hears it later
	// and fainter by its own distance, a fraction of a sample apart.
	speaker, depth := array.Vec{X: 0.004, Y: -0.003}, 0.03
	delay, gain := make([]float64, Mics), make([]float64, Mics)
	for m := range Mics {
		d := math.Hypot(arrayGeometry[m].Sub(speaker).Len(), depth)
		delay[m] = 2 + d/array.SpeedOfSound*Rate
		gain[m] = 0.6 * depth / d
	}
	talkGain := 0.6 * 6000 * math.Pow(10, talkerDB/20)
	at := func(x []float64, t float64) float64 {
		lo := int(t)
		f := t - float64(lo)
		return x[lo]*(1-f) + x[lo+1]*f
	}

	var sc playbackScene
	frame, ref := make([][]int16, Mics), []int16(nil)
	for i := range n {
		on := !bursts || (i/Rate)%2 == 0
		for m := range Mics {
			v := gain[m]*at(play, float64(i+32)-delay[m]) + r.NormFloat64()*20
			if on && !math.IsInf(talkerDB, -1) {
				v += talkGain * at(talk, float64(i+32)-arrayGeometry.Delay(m, bearing)*Rate)
			}
			frame[m] = append(frame[m], int16(v))
		}
		ref = append(ref, int16(play[i+32]))
		if len(ref) == 320 {
			sc.mics = append(sc.mics, frame)
			sc.refs = append(sc.refs, ref)
			frame, ref = make([][]int16, Mics), nil
		}
	}
	return sc
}

// raw is frame i as the converter delivers it: the microphones, then the loopback, S24_3LE.
func (sc playbackScene) raw(i int) []byte {
	b := make([]byte, 0, len(sc.refs[i])*Channels*3)
	put := func(v int16) {
		x := int32(v) << 8
		b = append(b, byte(x), byte(x>>8), byte(x>>16))
	}
	for s := range sc.refs[i] {
		for c := range Channels {
			switch {
			case c < Mics:
				put(sc.mics[i][c][s])
			case c == RefFirst:
				put(sc.refs[i][s])
			default:
				put(0)
			}
		}
	}
	return b
}

// The finder hears a talker through the device's own music, which is louder than they are at the
// microphones, by taking the speaker out with what it learns from the loopback. Without that it would
// point at the speaker, or be told to give up.
func TestFinderHearsThroughPlayback(t *testing.T) {
	for _, deg := range []float64{30, 150, 260} {
		sc := newPlaybackScene(array.Radians(deg), -6, true, 6, uint64(deg)+3)
		f := newFinder()
		for i, fr := range sc.mics {
			f.lookRef(fr, sc.refs[i], true)
		}
		if f.bearing < 0 {
			t.Errorf("talker at %v° 6 dB under the speaker: no bearing", deg)
			continue
		}
		if d := math.Abs(math.Remainder(array.Degrees(f.bearing)-deg, 360)); d > 20 {
			t.Errorf("talker at %v° 6 dB under the speaker: facing %.0f°", deg, array.Degrees(f.bearing))
		}
	}
}

// The superdirective mix's null takes the speaker out of what it passes while keeping the talker it
// is held on: the speaker alone comes through far under the center microphone, and the talker, which is
// what is left between the mix with them and without, at about their own level.
func TestSteeredNullsTheSpeaker(t *testing.T) {
	const talkerDB = -10
	with := newPlaybackScene(0, talkerDB, false, 6, 21)
	alone := newPlaybackScene(0, math.Inf(-1), false, 6, 21)
	a, b := newSteered(), newSteered()

	var speaker, center, talker, ref float64
	settled := len(with.mics) - 100 // the last two seconds, the null long since learned
	for i := range with.mics {
		ya := a.MixRaw(with.raw(i), with.mics[i])
		yb := b.MixRaw(alone.raw(i), alone.mics[i])
		if !a.playing || !b.playing {
			t.Fatalf("frame %d: the loopback was not taken for playback", i)
		}
		if i < settled {
			continue
		}
		for s := range yb {
			speaker += float64(yb[s]) * float64(yb[s])
			c := float64(alone.mics[i][1][s])
			center += c * c
			d := float64(ya[s]) - float64(yb[s])
			talker += d * d
			tc := float64(with.mics[i][1][s]) - c
			ref += tc * tc
		}
	}
	if a.Beam() != 0 {
		t.Fatalf("the beam turned to %d while playing", a.Beam())
	}
	if db := 10 * math.Log10(speaker/center); db > -15 {
		t.Errorf("the speaker comes through %.1f dB against the center microphone, want under -15", db)
	}
	if db := 10 * math.Log10(talker/ref); math.Abs(db) > 3 {
		t.Errorf("the talker comes through at %.1f dB of their level", db)
	}
}
