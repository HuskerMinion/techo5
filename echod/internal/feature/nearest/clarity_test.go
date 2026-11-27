package nearest

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A wake this device wins against the others opens its turn with how clearly it was heard, as measured
// and before the offset, for the turn's event to report (#727).
func TestAWonWakeCarriesItsClarity(t *testing.T) {
	h := newHouse(t)
	if err := config.Set().Nearest().Offset(5); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.Set().Nearest().Offset(0) })
	h.n.score = func() float64 { return 13.7 }
	h.n.Woke(0)
	h.wait(300 * time.Millisecond)
	if len(h.clarity) != 1 || h.clarity[0] != 13.7 {
		t.Fatalf("opened with clarity %v, want [13.7]: the measurement, not the claim", h.clarity)
	}
}

// One answered at once, with Nearest device off, measured nothing and opens with no clarity.
func TestAWakeNotWeighedCarriesNoClarity(t *testing.T) {
	h := newHouse(t)
	h.n.enabled = false
	measured := false
	h.n.score = func() float64 { measured = true; return 13.7 }
	h.n.Woke(0)
	if h.at0 != 1 || len(h.clarity) != 0 {
		t.Fatalf("off: at once %d, clarity %v; want at once and no clarity", h.at0, h.clarity)
	}
	if measured {
		t.Error("measured how clearly a wake was heard with Nearest device off")
	}
}

// A faint win is asked about with its clarity, which the turn reports once Home Assistant has heard it.
func TestAFaintWinCarriesItsClarity(t *testing.T) {
	h := newHouse(t)
	confirmBelow(t, true, 8)
	var got []float64
	h.n.faint = func(_ int, _ time.Time, clarity float64) { got = append(got, clarity) }
	h.n.score = func() float64 { return 3.7 }
	h.n.Woke(0)
	h.wait(300 * time.Millisecond)
	if len(got) != 1 || got[0] != 3.7 {
		t.Fatalf("asked about with clarity %v, want [3.7]", got)
	}
}

// A confirmed wake says whether its answer came of a claim: only then was its clarity weighed.
func TestAConfirmedWakeSaysWhetherItWasClaimed(t *testing.T) {
	h := newHouse(t)
	var answer, claimed *bool
	h.n.Confirmed(0, h.now, 11, 11, nil, func(a, c bool) { answer, claimed = &a, &c })
	h.wait(300 * time.Millisecond)
	if answer == nil || !*answer || !*claimed {
		t.Fatalf("won: answer %v, claimed %v; want both", answer, claimed)
	}

	h = newHouse(t)
	h.n.enabled = false
	answer, claimed = nil, nil
	h.n.Confirmed(0, h.now, 11, 11, nil, func(a, c bool) { answer, claimed = &a, &c })
	if answer == nil || !*answer || *claimed {
		t.Fatalf("off: answer %v, claimed %v; want an answer and no claim", answer, claimed)
	}
}
