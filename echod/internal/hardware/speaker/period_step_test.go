//go:build !panel

package speaker

import "testing"

// With no step the card is asked for the player's own period, as on every Echo; with one, for the
// next multiple of it. 480 is the step a card that takes periods in 10 ms blocks at 48 kHz would set.
func TestTheCardsPeriodIsRoundedUpToItsStep(t *testing.T) {
	defer func(n int) { periodStep = n }(periodStep)

	periodStep = 0
	if got := hwPeriod(768); got != 768 {
		t.Errorf("no step: hwPeriod(768) = %d, want 768", got)
	}
	periodStep = 480
	for n, want := range map[int]int{768: 960, 480: 480, 960: 960, 1: 480, 1536: 1920} {
		if got := hwPeriod(n); got != want {
			t.Errorf("step 480: hwPeriod(%d) = %d, want %d", n, got, want)
		}
	}
}
