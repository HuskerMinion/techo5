package mic

import (
	"math"
	"math/rand"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/denoise"
)

// A quiet room through noise reduction and then leveling, as a request is between its words (#662):
// what noise reduction leaves of it can be under one sample step, and rounded to whole steps before the
// leveling it became runs of exact zeros that no gain brings back. With noise reduction's floor at the
// -20 dB it was meant to be (it was -40) and leveling from the unrounded samples, it stays the faint room
// it was.
func TestAQuietRoomIsNotLeveledFromZeros(t *testing.T) {
	longestZeros := func(keep bool, sigma float64) (longest, zeros int) {
		d := denoise.NewStream(Rate)
		l := newLeveler()
		rng := rand.New(rand.NewSource(7))
		frame := make([]int16, FrameSamples)
		exact := make([]float32, FrameSamples)
		run := 0
		for k := range 300 { // six seconds: a second of room, two of speech, three of room
			for i := range frame {
				v := sigma * rng.NormFloat64()
				if k >= 50 && k < 150 {
					v += 3000 * math.Sin(2*math.Pi*300*float64(k*FrameSamples+i)/Rate)
				}
				frame[i] = int16(math.Round(v))
			}
			if keep {
				d.ApplyKeeping(frame, exact)
				l.applyFrom(frame, exact)
			} else {
				d.Apply(frame)
				l.apply(frame)
			}
			if k < 150 {
				continue // the room after the speech, once the gain has been set by it
			}
			for _, s := range frame {
				if s == 0 {
					zeros++
					run++
					longest = max(longest, run)
				} else {
					run = 0
				}
			}
		}
		return longest, zeros
	}
	// From a room of two sample steps up, no run as long as 20 ms; and leveling from the unrounded
	// samples never leaves more zeros than leveling from the rounded ones did.
	for _, sigma := range []float64{2, 4, 15} {
		rounded, roundedZeros := longestZeros(false, sigma)
		kept, keptZeros := longestZeros(true, sigma)
		t.Logf("room %.0f steps: longest zeros rounded first %d (%d in all), kept %d (%d in all)", sigma, rounded, roundedZeros, kept, keptZeros)
		if kept >= Rate/50 {
			t.Errorf("room %.0f steps: a run of %d exact zeros (%.0f ms); want none as long as 20 ms", sigma, kept, float64(kept)*1000/Rate)
		}
		if keptZeros > roundedZeros {
			t.Errorf("room %.0f steps: %d zeros leveled from the exact samples, %d from the rounded", sigma, keptZeros, roundedZeros)
		}
	}
}
