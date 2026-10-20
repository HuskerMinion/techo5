package voice

import (
	"math"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
)

func tone(ms int, amp float64) []int16 {
	n := ms * mic.Rate / 1000
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(amp * math.Sin(2*math.Pi*400*float64(i)/float64(mic.Rate)))
	}
	return out
}

func cat(parts ...[]int16) []int16 {
	var out []int16
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// The end of the word, then a pause: what is sent starts at the pause, so recognition does not hear
// the word's last sound as a word.
func TestHistoryAfterAPauseLosesTheWakeWordsEnd(t *testing.T) {
	pre := cat(tone(100, 8000), tone(150, 50))
	got := afterWakeTail(pre)
	if want := len(pre) - 100*mic.Rate/1000; len(got) != want {
		t.Fatalf("kept %d samples, want %d (from the pause on)", len(got), want)
	}
}

// The request runs straight on from the word: nothing to tell them apart by, the history is kept.
func TestHistoryWithoutAPauseIsKept(t *testing.T) {
	pre := cat(tone(100, 8000), tone(20, 50), tone(130, 6000))
	if got := afterWakeTail(pre); len(got) != len(pre) {
		t.Fatalf("kept %d of %d samples", len(got), len(pre))
	}
}

// Quiet throughout (nothing loud before the quiet) is not a word's end to cut.
func TestQuietHistoryIsKept(t *testing.T) {
	pre := tone(250, 50)
	if got := afterWakeTail(pre); len(got) != len(pre) {
		t.Fatalf("kept %d of %d samples", len(got), len(pre))
	}
}
