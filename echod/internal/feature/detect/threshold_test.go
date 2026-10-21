package detect

import "testing"

// The stop word is judged with the same allowance for the speaker as every other word.
//
// While the canceller runs, what reaches the detector is the residual and the word scores lower, so
// every slot's threshold drops by playingSlack. The stop word used to return before that adjustment
// was applied, which made the one word whose job is to interrupt a sound the only word judged with
// no allowance for the sound — and it is the one word always said over a playing speaker.
func TestTheStopWordGetsTheSlackTheOtherSlotsGet(t *testing.T) {
	base := func(int) float64 { return 0.8 }
	const stop = 0.7

	quiet := thresholdFor(StopSlot, base, stop, false)
	if quiet != stop {
		t.Errorf("stop threshold in a quiet room is %v, want the configured %v", quiet, stop)
	}

	playing := thresholdFor(StopSlot, base, stop, true)
	if want := stop - playingSlack; playing != want {
		t.Errorf("stop threshold while the canceller runs is %v, want %v", playing, want)
	}

	// The same drop the other slots get, so the stop word is no harder to say over a speaker than
	// the wake word is.
	other := base(0) - thresholdFor(0, base, stop, true)
	if got := stop - playing; got != other {
		t.Errorf("the stop word drops by %v while slot 0 drops by %v; they should match", got, other)
	}
}

// The volume down word is judged at its own threshold with or without the canceller: it is only
// listened for over music, and acts with no turn after it to show a mistake.
func TestTheVolumeDownWordGetsNoSlack(t *testing.T) {
	base := func(slot int) float64 {
		if slot == CommandSlot {
			return 0.9
		}
		return 0.8
	}
	for _, canceling := range []bool{false, true} {
		if got := thresholdFor(CommandSlot, base, 0.7, canceling); got != 0.9 {
			t.Errorf("canceling=%v: judged at %v, want its own 0.9", canceling, got)
		}
	}
	if got, want := thresholdFor(0, base, 0.7, true), base(0)-playingSlack; got != want {
		t.Errorf("the wake word is judged at %v, want %v with the slack", got, want)
	}
}

// The slack never drags a threshold below the floor, however low it was configured.
func TestTheSlackStopsAtTheFloor(t *testing.T) {
	base := func(int) float64 { return 0.52 }

	if got := thresholdFor(StopSlot, base, 0.52, true); got != slackFloor {
		t.Errorf("a stop threshold of 0.52 with slack is %v, want the floor %v", got, slackFloor)
	}
	if got := thresholdFor(0, base, 0.7, true); got != slackFloor {
		t.Errorf("a wake threshold of 0.52 with slack is %v, want the floor %v", got, slackFloor)
	}
}

// A slot that is not the stop word is judged against its own threshold, not the stop word's.
func TestTheSlotsKeepTheirOwnThresholds(t *testing.T) {
	base := func(slot int) float64 { return 0.6 + float64(slot)/100 }

	for _, slot := range []int{0, 1, 2} {
		if got, want := thresholdFor(slot, base, 0.7, false), base(slot); got != want {
			t.Errorf("slot %d judged at %v, want its own %v", slot, got, want)
		}
	}
}
