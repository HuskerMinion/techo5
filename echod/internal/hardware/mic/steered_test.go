//go:build dot && cannoli

package mic

import (
	"encoding/binary"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
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
		f.act.floor = 1
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
