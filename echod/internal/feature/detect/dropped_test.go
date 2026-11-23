package detect

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// failing is a model that loads and then fails every inference, as one using an operator the engine
// lacks does: no panic, only an error and a score of nothing.
type failing struct{}

func (failing) ProcessFeatures([][]int8) bool { return false }
func (failing) SlidingAverage() float64       { return 0 }
func (failing) Err() error                    { return errors.New("unsupported MUL") }

func TestAModelThatFailsEveryInferenceIsDropped(t *testing.T) {
	b := &microBackend{}
	feat := [][]int8{make([]int8, 40)}
	for i := 1; i < failsToDrop; i++ {
		if !b.run("w", failing{}, feat) {
			t.Fatalf("dropped after %d failed inferences, before %d", i, failsToDrop)
		}
	}
	if b.run("w", failing{}, feat) {
		t.Error("a model whose every inference fails was kept, scoring nothing")
	}
}

// flaky fails when told to, and runs otherwise, as a detector's Err reports only the last inference.
type flaky struct{ fail *bool }

func (flaky) ProcessFeatures([][]int8) bool { return false }
func (flaky) SlidingAverage() float64       { return 0 }
func (f flaky) Err() error {
	if *f.fail {
		return errors.New("a bad frame")
	}
	return nil
}

func TestAModelThatFailsNowAndThenIsKept(t *testing.T) {
	b := &microBackend{}
	feat := [][]int8{make([]int8, 40)}
	fail := false
	for i := range 10 * failsToDrop {
		fail = i%(failsToDrop/2) == 0 // a bad frame now and then, never a run of them
		if !b.run("w", flaky{&fail}, feat) {
			t.Fatalf("dropped at inference %d for failing now and then", i)
		}
	}
}

func TestWakeWordProblemCutsOnACharacter(t *testing.T) {
	d := &Detect{wakeProblem: newWakeProblemEntity()}
	d.setWakeProblem([]string{strings.Repeat("é", 200)}) // two bytes each: byte 252 can split one
	if got := d.wakeProblem.Get(); !utf8.ValidString(got) || !strings.HasSuffix(got, "...") {
		t.Errorf("not valid UTF-8 or not cut: %q", got[len(got)-8:])
	}
}

func TestADroppedWakeWordIsPutBackOnceAndThenHeld(t *testing.T) {
	var scheduled []time.Duration
	was := later
	later = func(d time.Duration, f func()) { scheduled = append(scheduled, d) }
	t.Cleanup(func() { later = was })

	d := &Detect{wakeProblem: newWakeProblemEntity()}
	d.setWakeProblem(nil)
	t0 := time.Now()

	d.wakeDropped(0, "okay_nabu", t0)
	if len(scheduled) != 1 || scheduled[0] != reloadAfter {
		t.Fatalf("put back after %v, want once after %v", scheduled, reloadAfter)
	}
	if got := d.wakeProblem.Get(); !strings.Contains(got, "Wake word 1 (okay_nabu)") || !strings.Contains(got, "putting it back") {
		t.Errorf("problem says %q", got)
	}

	// Failing again soon after: held, not reloaded in a loop, and said so.
	d.wakeDropped(0, "okay_nabu", t0.Add(time.Minute))
	if len(scheduled) != 1 {
		t.Errorf("reloaded again %d times", len(scheduled)-1)
	}
	if got := d.wakeProblem.Get(); !strings.Contains(got, "keeps failing") {
		t.Errorf("problem says %q", got)
	}

	// Long after: a fresh failure is put back again.
	d.wakeDropped(0, "okay_nabu", t0.Add(time.Minute+holdFor+time.Second))
	if len(scheduled) != 2 {
		t.Errorf("a failure long after was not put back: %d", len(scheduled))
	}
}
