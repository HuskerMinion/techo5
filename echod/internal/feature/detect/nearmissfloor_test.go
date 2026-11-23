package detect

import (
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// While the speaker's allowance is off its threshold, the stop word's floor sits stopNearMissBelow under
// its cutoff, rounded to hundredths, never over NearMiss and never under stopNearMissLeast. Without the
// allowance, and for every other slot, it is NearMiss, whatever the cutoff.
func TestTheStopWordsNearMissFloorFollowsItsCutoff(t *testing.T) {
	// What a threshold set from Home Assistant is: a float32, and the cutoff made from it over playback.
	ha := func(threshold float32) float64 { return float64(threshold) }
	playing := func(threshold float32) float64 { return thresholdFor(StopSlot, nil, ha(threshold), true) }
	for _, c := range []struct {
		why    string
		slot   int
		cutoff float64
		slack  bool
		want   float64
	}{
		{"over playback at any threshold from 0.50 to 0.60", StopSlot, 0.50, true, 0.35},
		{"over playback at 0.65", StopSlot, 0.55, true, 0.40},
		{"over playback at the default", StopSlot, 0.60, true, 0.45},
		{"over playback at 0.75", StopSlot, 0.65, true, NearMiss},
		{"over playback at 1.00", StopSlot, 0.90, true, NearMiss},
		{"a cutoff lower than any threshold makes", StopSlot, 0.40, true, stopNearMissLeast},
		{"the default from Home Assistant, over playback", StopSlot, playing(0.70), true, 0.45},
		{"0.55 from Home Assistant, over playback", StopSlot, playing(0.55), true, 0.35},
		{"0.75 from Home Assistant, over playback", StopSlot, playing(0.75), true, NearMiss},
		{"a cutoff a hair under 0.65", StopSlot, ha(0.65), true, NearMiss},
		{"a cutoff a hair over 0.55", StopSlot, ha(0.55), true, 0.40},
		{"a quiet room at 0.55", StopSlot, 0.55, false, NearMiss},
		{"a quiet room at 0.50", StopSlot, 0.50, false, NearMiss},
		{"a wake word", 0, 0.50, true, NearMiss},
		{"the other wake word", 1, 0.55, true, NearMiss},
		{"the hang-up word", HangupSlot, 0.50, true, NearMiss},
		{"a command word", CommandSlot, 0.50, true, NearMiss},
	} {
		// Exactly, not within a tolerance: the floor is printed in the log as it is.
		if got := stopNearMissFloor(c.slot, c.cutoff, c.slack); got != c.want {
			t.Errorf("%s: slot %d against %v: floor %v, want %v", c.why, c.slot, c.cutoff, got, c.want)
		}
	}
}

// floorRig is what the engine's thresholds and the stop word's floor are asked about: the cutoff every
// slot is judged against, and whether it has the allowance for the speaker taken off.
type floorRig struct {
	cutoff  float64
	playing bool
}

// stopRig is an engine with the stop word in its slot and a wake word in slot 0, both judged against
// the rig's cutoff, with the stop word's floor as the device has it, and every near miss the engine
// passes on.
func stopRig(t *testing.T, r *floorRig) (*Engine, chan int) {
	t.Helper()
	fakes(t)
	e := New(StopSlot+1, nil)
	e.Threshold = func(int) float64 { return r.cutoff }
	e.NearMissFloor = func(slot int, cutoff float64) float64 { return stopNearMissFloor(slot, cutoff, r.playing) }
	if err := e.Use(StopSlot, model("stop", wake.KindMicroWakeWord)); err != nil {
		t.Fatalf("Use(StopSlot): %v", err)
	}
	if err := e.Use(0, model("hey_jarvis", wake.KindMicroWakeWord)); err != nil {
		t.Fatalf("Use(0): %v", err)
	}
	told := make(chan int, 8)
	e.OnNearMiss = func(slot int, _ float64) { told <- slot }
	return e, told
}

// judgeSlot is a score of slot n as the engine judges one, with its lock held.
func judgeSlot(e *Engine, n int, score float64, at time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.judge(n, &e.slots[n], score, at, nil)
}

// tryAt is an utterance of slot n that peaks at peak and falls away, settled once NearMissSettle has
// gone by.
func tryAt(e *Engine, n int, peak float64, at time.Time) {
	judgeSlot(e, n, peak, at)
	judgeSlot(e, n, 0.1, at.Add(NearMissSettle+50*time.Millisecond))
}

// nearMisses is the near miss events the board has for slot n, as the log numbers it.
func nearMisses(e *Engine, slot int) []wakeword.ScoreEvent {
	var out []wakeword.ScoreEvent
	for _, ev := range e.board.read(0, 0, time.Now(), nil).events {
		if ev.What == "near miss" && ev.Slot == slot+1 {
			out = append(out, ev)
		}
	}
	return out
}

// nearMissLines is the "wake near miss" lines in what was logged.
func nearMissLines(log string) []string {
	var out []string
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, `msg="wake near miss"`) {
			out = append(out, line)
		}
	}
	return out
}

// noneTold fails if the engine passed a near miss on to what acts on one.
func noneTold(t *testing.T, told chan int, why string) {
	t.Helper()
	select {
	case slot := <-told:
		t.Errorf("%s: slot %d's near miss was passed on to the ducker and the clips", why, slot)
	case <-time.After(50 * time.Millisecond):
	}
}

// Over playback the stop word's cutoff is 0.50, NearMiss itself, and a "stop" that came close used to
// leave no line. Now it does, with the floor it was judged by, and it goes no further: the ducker, the
// ring's hush and the clips are not told about it.
func TestAStopThatCameCloseOverPlaybackIsLoggedAndNothingElse(t *testing.T) {
	logs := logged(t)
	e, told := stopRig(t, &floorRig{cutoff: 0.50, playing: true})
	tryAt(e, StopSlot, 0.42, time.Now())

	got := nearMisses(e, StopSlot)
	if len(got) != 1 || got[0].Peak != 0.42 || got[0].Cutoff != 0.50 || got[0].ID != "stop" || got[0].Slot != StopSlot+1 {
		t.Fatalf("near misses %+v, want the stop word's one at 0.42 against 0.50", got)
	}
	lines := nearMissLines(logs.String())
	if len(lines) != 1 || !strings.Contains(lines[0], "slot=101 id=stop peak=0.42 cutoff=0.5 ") ||
		!strings.HasSuffix(lines[0], " floor=0.35") {
		t.Errorf("near miss lines %q, want one at 0.42 against 0.5 with floor=0.35 at its end", lines)
	}
	noneTold(t, told, "a peak under NearMiss")
}

// A wake word against the same cutoff keeps the floor it always had: the same peak leaves nothing.
func TestAWakeWordKeepsTheOldFloor(t *testing.T) {
	e, told := stopRig(t, &floorRig{cutoff: 0.50, playing: true})
	tryAt(e, 0, 0.42, time.Now())

	if got := nearMisses(e, 0); len(got) != 0 {
		t.Errorf("a wake word's peak of 0.42 was reported: %+v", got)
	}
	noneTold(t, told, "a wake word's peak under NearMiss")
}

// In a quiet room the stop word keeps the floor it always had too, at the threshold this feature was
// made for: whatever the television scores on the stop model under 0.5 leaves no line, and a near miss
// from 0.5 up reads as it always did, with no floor in it.
func TestAStopInAQuietRoomKeepsTheOldFloor(t *testing.T) {
	logs := logged(t)
	e, told := stopRig(t, &floorRig{cutoff: 0.55})
	at := time.Now()
	tryAt(e, StopSlot, 0.42, at)
	if got := nearMisses(e, StopSlot); len(got) != 0 {
		t.Fatalf("a quiet room's 0.42 against 0.55 was reported: %+v", got)
	}
	noneTold(t, told, "a quiet room's peak under NearMiss")

	tryAt(e, StopSlot, 0.52, at.Add(5*time.Second))
	if got := nearMisses(e, StopSlot); len(got) != 1 || got[0].Peak != 0.52 {
		t.Errorf("near misses %+v, want the one at 0.52", got)
	}
	if lines := nearMissLines(logs.String()); len(lines) != 1 || strings.Contains(lines[0], "floor=") {
		t.Errorf("near miss lines %q, want one without a floor", lines)
	}
	select {
	case <-told:
	case <-time.After(time.Second):
		t.Error("a quiet room's near miss at 0.52 was not passed on")
	}
}

// A stop word's near miss from NearMiss up is passed on exactly as before, for the ring's hush above
// all: that is what lets a second "stop" through a ringing alarm. Its line is the one it always was,
// with no floor in it, though the floor over playback here is 0.45.
func TestAStopNearMissOverTheOldFloorIsStillPassedOn(t *testing.T) {
	logs := logged(t)
	e, told := stopRig(t, &floorRig{cutoff: 0.60, playing: true})
	tryAt(e, StopSlot, 0.55, time.Now())

	select {
	case slot := <-told:
		if slot != StopSlot {
			t.Errorf("slot %d was passed on, want the stop word's", slot)
		}
	case <-time.After(time.Second):
		t.Fatal("a stop near miss at 0.55 was not passed on")
	}
	if got := nearMisses(e, StopSlot); len(got) != 1 || got[0].Cutoff != 0.60 {
		t.Errorf("near misses %+v, want one against 0.60", got)
	}
	if lines := nearMissLines(logs.String()); len(lines) != 1 || strings.Contains(lines[0], "floor=") {
		t.Errorf("near miss lines %q, want one without a floor", lines)
	}
}

// The floor is the one in force when the word peaked, and so is the cutoff its line and the setup page
// give: a "stop" said as a reply ends peaks against the cutoff for playing, and is settled after the
// threshold has gone back up for the quiet room, where its 0.42 would not have been a near miss at all.
func TestTheFloorIsTheOneTheWordPeakedAgainst(t *testing.T) {
	logs := logged(t)
	r := &floorRig{cutoff: 0.50, playing: true}
	e, told := stopRig(t, r)
	at := time.Now()
	judgeSlot(e, StopSlot, 0.42, at)
	*r = floorRig{cutoff: 0.60} // the reply ended
	judgeSlot(e, StopSlot, 0.1, at.Add(NearMissSettle+50*time.Millisecond))

	got := nearMisses(e, StopSlot)
	if len(got) != 1 || got[0].Peak != 0.42 || got[0].Cutoff != 0.50 {
		t.Fatalf("near misses %+v, want the 0.42 said over the reply, against the reply's 0.50", got)
	}
	lines := nearMissLines(logs.String())
	if len(lines) != 1 || !strings.Contains(lines[0], " cutoff=0.5 ") || !strings.HasSuffix(lines[0], " floor=0.35") {
		t.Errorf("near miss lines %q, want one against 0.5 with floor=0.35", lines)
	}
	noneTold(t, told, "a peak under NearMiss")
}

// The default threshold set from Home Assistant comes as a float32, and so does the cutoff over
// playback made from it; the floor in the line is still a round one.
func TestTheFloorInTheLogIsRound(t *testing.T) {
	logs := logged(t)
	e, _ := stopRig(t, &floorRig{cutoff: thresholdFor(StopSlot, nil, float64(float32(0.70)), true), playing: true})
	tryAt(e, StopSlot, 0.46, time.Now())

	if lines := nearMissLines(logs.String()); len(lines) != 1 || !strings.HasSuffix(lines[0], " floor=0.45") {
		t.Errorf("near miss lines %q, want one with floor=0.45", lines)
	}
}

// A peak that settled under its floor is let go of. One left over from a quiet room would otherwise
// stand over the next try, said over a reply, that peaked lower than it but over that try's own floor,
// and the try would leave no line; nor is the old peak itself reported once the floor comes down.
func TestAPeakUnderTheFloorIsLetGoOf(t *testing.T) {
	r := &floorRig{cutoff: 0.55} // a quiet room: a floor of 0.50
	e, told := stopRig(t, r)
	at := time.Now()
	tryAt(e, StopSlot, 0.45, at)
	if got := nearMisses(e, StopSlot); len(got) != 0 {
		t.Fatalf("a 0.45 under a floor of 0.50 was reported: %+v", got)
	}

	*r = floorRig{cutoff: 0.50, playing: true} // a reply starts: a floor of 0.35
	tryAt(e, StopSlot, 0.40, at.Add(5*time.Second))
	got := nearMisses(e, StopSlot)
	if len(got) != 1 || got[0].Peak != 0.40 {
		t.Errorf("near misses %+v, want the one at 0.40 said over the reply, and not the old 0.45", got)
	}
	noneTold(t, told, "peaks under NearMiss")
}

// A word that never scored anything is not a near miss at zero, whatever floor a slot has.
func TestNothingScoredIsNoNearMiss(t *testing.T) {
	e, told := stopRig(t, &floorRig{cutoff: 0.50, playing: true})
	e.NearMissFloor = func(int, float64) float64 { return 0 }
	at := time.Now()
	judgeSlot(e, StopSlot, 0, at)
	judgeSlot(e, StopSlot, 0, at.Add(NearMissSettle+50*time.Millisecond))
	if got := nearMisses(e, StopSlot); len(got) != 0 {
		t.Errorf("silence was reported as a near miss: %+v", got)
	}
	noneTold(t, told, "silence")
}

// A slot's floor can only add lines: one set over NearMiss is taken as NearMiss.
func TestAFloorOverNearMissIsNearMiss(t *testing.T) {
	e, told := stopRig(t, &floorRig{cutoff: 0.90})
	e.NearMissFloor = func(int, float64) float64 { return 0.8 }
	tryAt(e, 0, 0.6, time.Now())
	if got := nearMisses(e, 0); len(got) != 1 {
		t.Errorf("near misses %+v, want the 0.6 a floor of NearMiss reports", got)
	}
	select {
	case <-told:
	case <-time.After(time.Second):
		t.Error("a near miss at 0.6 was not passed on")
	}
}
