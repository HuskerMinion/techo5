// Command beamcal works out where an array's microphones are from recordings of a sound played toward
// it from known places round the LED ring.
//
// Each file is one place: seg-<n>*.s32 for a source in the direction of LED segment n, a native dump
// (the daemon's /run/techo5/rawnative: S32 little-endian, interleaved). For every pair it measures the
// arrival difference with GCC-PHAT, fits the positions that explain them all (lib/array.Fit), and then
// asks the fitted array where each recording came from, which is the check that matters.
//
//	beamcal -rate 96000 -channels 6 -mics 0,1,2 seg-*.s32
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/lib/array"
)

func main() {
	rate := flag.Int("rate", 96000, "sample rate of the dumps")
	channels := flag.Int("channels", 6, "interleaved channels in the dumps")
	micList := flag.String("mics", "0,1,2", "which channels are microphones")
	skip := flag.Float64("skip", 0.5, "seconds to skip at the start of each file")
	segments := flag.Int("segments", 12, "LED segments round the ring")
	lo := flag.Float64("lo", 300, "lowest frequency used, Hz")
	hi := flag.Float64("hi", 7000, "highest frequency used, Hz")
	limitMM := flag.Float64("aperture", 60, "largest pair spacing searched, mm")
	flag.Parse()

	var mics []int
	for _, s := range strings.Split(*micList, ",") {
		m, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			fail("bad -mics: %v", err)
		}
		mics = append(mics, m)
	}

	segRE := regexp.MustCompile(`seg-(\d+)`)
	type take struct {
		seg     int
		bearing float64
		audio   [][]float32
	}
	var takes []take
	for _, path := range flag.Args() {
		m := segRE.FindStringSubmatch(filepath.Base(path))
		if m == nil {
			fail("%s: name has no seg-<n>", path)
		}
		seg, _ := strconv.Atoi(m[1])
		audio, err := load(path, *channels, mics, int(*skip*float64(*rate)))
		if err != nil {
			fail("%s: %v", path, err)
		}
		takes = append(takes, take{seg, array.Radians(float64(seg) * 360 / float64(*segments)), audio})
	}
	sort.Slice(takes, func(i, j int) bool { return takes[i].seg < takes[j].seg })
	if len(takes) < 3 {
		fail("need takes from at least three segments")
	}

	const n = 4096
	spec := array.NewSpectra(n, float64(*rate), *lo, *hi)
	pairs := array.Pairs(len(mics))
	limit := *limitMM / 1000 / array.SpeedOfSound

	fmt.Printf("%-4s", "seg")
	for m := range mics {
		fmt.Printf("  %7s", fmt.Sprintf("dB%d", mics[m]))
	}
	for _, p := range pairs {
		fmt.Printf("  %14s", fmt.Sprintf("τ%d-%d µs (str)", mics[p.A], mics[p.B]))
	}
	fmt.Println()

	var obs []array.Observation
	for _, t := range takes {
		acc := make([][]complex128, len(pairs))
		for p := range acc {
			acc[p] = make([]complex128, spec.Bins())
		}
		x := make([][]complex64, len(mics))
		for m := range x {
			x[m] = make([]complex64, spec.Bins())
		}
		for f := 0; f+n <= len(t.audio[0]); f += n / 2 {
			for m := range mics {
				spec.Into(t.audio[m][f:f+n], x[m])
			}
			for p, pair := range pairs {
				array.PHAT(acc[p], x[pair.A], x[pair.B], 1)
			}
		}
		fmt.Printf("%-4d", t.seg)
		for m := range mics {
			fmt.Printf("  %7.1f", rmsDB(t.audio[m]))
		}
		for p, pair := range pairs {
			lag, strength := spec.Lag(acc[p], limit, 0.25e-6)
			fmt.Printf("  %8.1f (%.2f)", lag*1e6, strength)
			obs = append(obs, array.Observation{P: pair, Bearing: t.bearing, TDOA: lag})
		}
		fmt.Println()
	}

	g, res, err := array.Fit(len(mics), obs)
	if err != nil {
		fail("%v", err)
	}
	fmt.Printf("\nfitted positions (mm, centred; bearing in degrees clockwise from segment 0), residual %.1f µs:\n", res*1e6)
	for m, p := range g {
		fmt.Printf("  ch%d  x %6.2f  y %6.2f   r %5.2f mm at %6.1f°\n", mics[m], p.X*1000, p.Y*1000,
			p.Len()*1000, array.Degrees(p.Bearing()))
	}
	fmt.Println("spacings:")
	for _, p := range pairs {
		fmt.Printf("  ch%d-ch%d  %5.2f mm\n", mics[p.A], mics[p.B], g[p.A].Sub(g[p.B]).Len()*1000)
	}
	fmt.Printf("Go: array.Geometry{")
	for i, p := range g {
		if i > 0 {
			fmt.Print(", ")
		}
		fmt.Printf("{%.5f, %.5f}", p.X, p.Y)
	}
	fmt.Println("}")

	fmt.Println("\nlocalized with the fitted array (16 kHz, as the daemon will):")
	for _, t := range takes {
		dec := decimate(t.audio, *rate/16000)
		l := array.NewLocator(g, 256, 16000, 500, 7000, 72)
		l.Decay = 1
		for f := 0; f+256 <= len(dec[0]); f += 128 {
			block := make([][]float32, len(dec))
			for m := range block {
				block[m] = dec[m][f : f+256]
			}
			l.Add(block, 1)
		}
		b, conf := l.Bearing()
		errDeg := math.Remainder(array.Degrees(b-t.bearing), 360)
		fmt.Printf("  seg %2d  at %5.1f°  found %5.1f°  error %+6.1f°  confidence %.2f\n",
			t.seg, array.Degrees(t.bearing), array.Degrees(b), errDeg, conf)
	}
}

// load reads the chosen channels of an interleaved S32 dump, full scale 1.
func load(path string, channels int, mics []int, skip int) ([][]float32, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	frames := len(raw) / (4 * channels)
	if frames <= skip {
		return nil, fmt.Errorf("only %d frames", frames)
	}
	out := make([][]float32, len(mics))
	for m, ch := range mics {
		out[m] = make([]float32, frames-skip)
		for i := skip; i < frames; i++ {
			v := int32(binary.LittleEndian.Uint32(raw[(i*channels+ch)*4:]))
			out[m][i-skip] = float32(v) / (1 << 31)
		}
	}
	return out, nil
}

// decimate takes every factor-th sample after a moving average as wide, which is crude but leaves
// the band below 7 kHz at 16 kHz with its phase intact, and phase is all a locator reads.
func decimate(in [][]float32, factor int) [][]float32 {
	if factor <= 1 {
		return in
	}
	out := make([][]float32, len(in))
	for m, x := range in {
		// Two passes of a windowed-sinc low pass at 7.2 kHz would be better; a 4-pole would shift
		// phase alike on every channel, which does not matter for differences. A box of factor
		// samples is linear phase and enough here.
		for i := 0; i+factor <= len(x); i += factor {
			var s float32
			for j := range factor {
				s += x[i+j]
			}
			out[m] = append(out[m], s/float32(factor))
		}
	}
	return out
}

func rmsDB(x []float32) float64 {
	var s float64
	for _, v := range x {
		s += float64(v) * float64(v)
	}
	return 10 * math.Log10(s/float64(len(x))+1e-20)
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "beamcal: "+format+"\n", a...)
	os.Exit(1)
}
