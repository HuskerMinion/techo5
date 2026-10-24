package detect

import (
	"runtime"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// scoresRig is one engine with a wake word in slot 0, judged against 0.9, and the Detect that reads it.
func scoresRig(t *testing.T) (*Engine, *Detect) {
	t.Helper()
	fakes(t)
	e := New(lastCommandSlot+1, nil)
	e.Threshold = func(int) float64 { return 0.9 }
	if err := e.Use(0, model("hey_jarvis", wake.KindMicroWakeWord)); err != nil {
		t.Fatalf("Use(0): %v", err)
	}
	return e, &Detect{engine: e, base: func(int) float64 { return 0.9 }}
}

// judged is a score of slot 0 as the engine judges one, with its lock held.
func judged(e *Engine, score float64, at time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.judge(0, &e.slots[0], score, at, nil)
}

// rowOf is slot 0's row in what was read.
func rowOf(t *testing.T, sc wakeword.Scores) wakeword.ScoreRow {
	t.Helper()
	for _, row := range sc.Rows {
		if row.Slot == 1 {
			return row
		}
	}
	t.Fatalf("no row for slot 1 in %+v", sc.Rows)
	return wakeword.ScoreRow{}
}

// highest is the best a row's peaks reached.
func highest(row wakeword.ScoreRow) float32 {
	var best float32
	for _, b := range row.Best {
		best = max(best, b)
	}
	return best
}

// eventsOf is the events of one kind in what was read.
func eventsOf(sc wakeword.Scores, what string) []wakeword.ScoreEvent {
	var out []wakeword.ScoreEvent
	for _, ev := range sc.Events {
		if ev.What == what {
			out = append(out, ev)
		}
	}
	return out
}

// The peaks from moment to moment are kept only while somebody watches: a score heard with no view up
// leaves nothing, and once a view has asked, the next one shows.
func TestScoresAreOnlyKeptWhileSomebodyWatches(t *testing.T) {
	e, d := scoresRig(t)
	judged(e, 0.4, time.Now())
	if got := highest(rowOf(t, d.Scores(0, 0, false))); got != 0 {
		t.Errorf("with nobody watching, a peak of %v was kept", got)
	}

	d.Scores(0, 0, true)
	judged(e, 0.6, time.Now())
	if got := highest(rowOf(t, d.Scores(0, 0, false))); got != float32(0.6) {
		t.Errorf("while watched, the peak read %v, want 0.6", got)
	}

	// Reading without watching does not start it again once it has run out.
	e.board.watchedUntil.Store(time.Now().Add(-time.Second).UnixNano())
	d.Scores(0, 0, false)
	if e.board.watchedUntil.Load() > time.Now().UnixNano() {
		t.Error("a read without watch started the peaks being kept")
	}
}

// Once the watch lapses, the first score forgets every peak, so nothing heard while somebody watched
// stays in memory after they stop.
func TestThePeaksAreForgottenOnceTheWatchLapses(t *testing.T) {
	e, d := scoresRig(t)
	d.Scores(0, 0, true)
	judged(e, 0.6, time.Now())
	e.board.watchedUntil.Store(time.Now().Add(-time.Second).UnixNano())
	judged(e, 0.1, time.Now())

	e.board.mu.Lock()
	left := e.board.traces
	e.board.mu.Unlock()
	if left != nil || e.board.kept.Load() {
		t.Errorf("after the watch lapsed and a score came, the peaks are still held: %v", left)
	}
	if got := highest(rowOf(t, d.Scores(0, 0, false))); got != 0 {
		t.Errorf("a peak of %v is still read", got)
	}
}

// Each quarter second keeps its highest score, so a peak between two polls is not lost to the frames
// around it.
func TestABucketKeepsItsHighestScore(t *testing.T) {
	e, d := scoresRig(t)
	d.Scores(0, 0, true)
	at := time.UnixMilli((time.Now().UnixMilli()/bucketMs)*bucketMs + 10)
	for _, s := range []float64{0.3, 0.7, 0.5} {
		judged(e, s, at)
		at = at.Add(20 * time.Millisecond)
	}
	judged(e, 0.2, at.Add(bucketMs*time.Millisecond))

	r := e.board.read(0, 0, at.Add(bucketMs*time.Millisecond), []int{0})
	best := r.best[0]
	if n := len(best); n < 2 || best[n-2] != float32(0.7) || best[n-1] != float32(0.2) {
		t.Errorf("the last two buckets read %v, want 0.7 then 0.2", best[max(0, len(best)-2):])
	}
}

// A slot's peaks go with its word: another word chosen into the slot, or the slot cleared, does not
// show what the word before scored.
func TestANewWordInASlotStartsWithoutTheOldScores(t *testing.T) {
	e, d := scoresRig(t)
	d.Scores(0, 0, true)
	judged(e, 0.95, time.Now())
	if err := e.Use(0, model("okay_nabu", wake.KindMicroWakeWord)); err != nil {
		t.Fatal(err)
	}
	row := rowOf(t, d.Scores(0, 0, true))
	if row.ID != "okay_nabu" || highest(row) != 0 {
		t.Errorf("the new word's row %s shows %v, want none of the old word's 0.95", row.ID, highest(row))
	}

	judged(e, 0.5, time.Now())
	e.Clear(0)
	r := e.board.read(0, 0, time.Now(), []int{0})
	for _, v := range r.best[0] {
		if v != 0 {
			t.Fatalf("after the slot was cleared, its peaks %v are still held", r.best[0])
		}
	}
}

// The events are kept with nobody watching: a page opened after the word was said still shows the near
// miss, with the cutoff it missed, as the log line has it.
func TestANearMissIsKeptWithTheCutoffItMissed(t *testing.T) {
	e, d := scoresRig(t)
	at := time.Now()
	judged(e, 0.78, at)
	judged(e, 0.1, at.Add(NearMissSettle+50*time.Millisecond))

	sc := d.Scores(0, 0, false)
	if len(sc.Events) != 1 {
		t.Fatalf("events %+v, want the one near miss", sc.Events)
	}
	ev := sc.Events[0]
	if ev.What != "near miss" || ev.Slot != 1 || ev.Peak != 0.78 || ev.Cutoff != 0.9 || ev.ID != "hey_jarvis" {
		t.Errorf("the near miss read %+v", ev)
	}
	if again := d.Scores(ev.Seq, 0, false); len(again.Events) != 0 {
		t.Errorf("reading after it gave it again: %+v", again.Events)
	}
}

// A detection is reported once its hold is over, with the peak it reached and the score and cutoff it
// crossed at, the same three numbers as the log's line. The cutoff is the one it crossed, even though
// the threshold drops while the hold runs, as it does when the wake tone starts the canceller.
func TestADetectionShowsItsPeakAndWhatItCrossed(t *testing.T) {
	e, d := scoresRig(t)
	at := time.Now()
	judged(e, 0.92, at)
	e.Threshold = func(int) float64 { return 0.8 }
	judged(e, 0.97, at.Add(100*time.Millisecond))
	if sc := d.Scores(0, 0, false); len(sc.Events) != 0 {
		t.Fatalf("reported %+v before the hold was over", sc.Events)
	}
	judged(e, 0.4, at.Add(Hold+time.Millisecond))

	sc := d.Scores(0, 0, false)
	if len(sc.Events) != 1 {
		t.Fatalf("events %+v, want the one detection", sc.Events)
	}
	if ev := sc.Events[0]; ev.What != "detected" || ev.Peak != 0.97 || ev.Crossing != 0.92 || ev.Cutoff != 0.9 {
		t.Errorf("the detection read %+v, want peak 0.97, crossing 0.92 against 0.9", ev)
	}
}

// A score between the confirm threshold and the word's own is the log's "wake candidate": it is listed as
// unsure, with the threshold it went over and the one it stayed under.
func TestAnUnsureWakeShowsBothThresholds(t *testing.T) {
	e, d := scoresRig(t)
	e.Candidate = func(int) float64 { return 0.7 }
	at := time.Now()
	judged(e, 0.75, at)
	judged(e, 0.3, at.Add(40*time.Millisecond))

	got := eventsOf(d.Scores(0, 0, false), "unsure")
	if len(got) != 1 || got[0].Peak != 0.75 || got[0].AskedFrom != 0.7 || got[0].Cutoff != 0.9 {
		t.Errorf("unsure events %+v, want one at 0.75 between 0.7 and 0.9", got)
	}
}

// A word's first score and its model being taken out for failing are listed too, with the slot the log
// names them by.
func TestReadyAndDroppedAreListed(t *testing.T) {
	dr := &dropper{fake: fake{loaded: map[string]float64{}}}
	was := build
	build = func(wake.Kind) (backend, error) { return dr, nil }
	t.Cleanup(func() { build = was })

	e := New(lastCommandSlot+1, nil)
	e.Threshold = func(int) float64 { return 0.9 }
	d := &Detect{engine: e, base: func(int) float64 { return 0.9 }}
	if err := e.Use(CommandSlot, model("turn_it_down", wake.KindMicroWakeWord)); err != nil {
		t.Fatal(err)
	}
	e.score(make([]int16, 320), nil)
	if got := eventsOf(d.Scores(0, 0, false), "ready"); len(got) != 1 || got[0].Slot != CommandSlot+1 || got[0].ID != "turn_it_down" {
		t.Errorf("ready events %+v, want turn_it_down in slot %d", got, CommandSlot+1)
	}

	dr.fail = []string{"turn_it_down"}
	e.score(make([]int16, 320), nil)
	if got := eventsOf(d.Scores(0, 0, false), "dropped"); len(got) != 1 || got[0].Slot != CommandSlot+1 {
		t.Errorf("dropped events %+v", got)
	}
}

// After a detection the slot stops reporting for the refractory, but it still scores, and a person
// watching sees those scores too, marked as held back, rather than a gap.
func TestScoresHeldBackAreShownAsSettling(t *testing.T) {
	e, d := scoresRig(t)
	d.Scores(0, 0, true)
	at := time.Now()
	judged(e, 0.95, at)
	judged(e, 0.3, at.Add(Hold+time.Millisecond))
	later := at.Add(Hold + 300*time.Millisecond)
	judged(e, 0.85, later)

	r := e.board.read(0, bucketOf(later), later, []int{0})
	if len(r.best[0]) != 1 || r.best[0][0] != float32(0.85) || !r.settling[0][0] {
		t.Errorf("during the refractory: best %v settling %v, want 0.85 held back", r.best[0], r.settling[0])
	}
}

// A view that fell behind further than the board reaches is told so, for the peaks and the events both,
// and gets what is still kept.
func TestAReadFromTooLongAgoIsToldItMissedSome(t *testing.T) {
	var b board
	now := time.Now()
	if r := b.read(0, bucketOf(now)-boardBuckets-5, now, nil); !r.lost || r.first != bucketOf(now)-boardBuckets+1 {
		t.Errorf("a read from before the trace: lost %v first %d", r.lost, r.first)
	}
	for range boardEvents + 6 {
		b.event(wakeword.ScoreEvent{What: "near miss"})
	}
	r := b.read(1, 0, now, nil)
	if !r.lost || len(r.events) != boardEvents || r.events[0].Seq != 7 {
		t.Errorf("after 70 events, a read from the first: lost %v, %d events from %d", r.lost, len(r.events), r.events[0].Seq)
	}
	if r := b.read(69, 0, now, nil); r.lost || len(r.events) != 1 {
		t.Errorf("a read from the last but one: lost %v, %d events", r.lost, len(r.events))
	}
}

// Each row names its word as a person would, and its slot as the log does: one more than the engine's
// index, so slot=104 on the page is slot=104 in the log.
func TestScoresNameEachWordAsTheLogDoes(t *testing.T) {
	for n, want := range map[int]string{
		0: "Wake word 1", 1: "Wake word 2", StopSlot: "Stop word", HangupSlot: "Hang-up word",
		CommandSlot: "Volume down word", VolumeUpSlot: "Volume up word", NextSongSlot: "Next song word",
	} {
		if got := roleOf(n); got != want {
			t.Errorf("roleOf(%d) = %q, want %q", n, got, want)
		}
	}

	e, d := scoresRig(t)
	if err := e.Use(VolumeUpSlot, model("turn_it_up", wake.KindMicroWakeWord)); err != nil {
		t.Fatal(err)
	}
	e.Threshold = func(n int) float64 {
		if n == VolumeUpSlot {
			return 0.75
		}
		return 0.8 // as the canceller's allowance makes it
	}
	d.base = func(n int) float64 {
		if n == VolumeUpSlot {
			return 0.75
		}
		return 0.9
	}
	sc := d.Scores(0, 0, false)
	if len(sc.Rows) != 2 {
		t.Fatalf("rows %+v, want the wake word and the volume up word", sc.Rows)
	}
	ww, up := sc.Rows[0], sc.Rows[1]
	if ww.Slot != 1 || ww.Threshold != 0.9 || ww.Cutoff != 0.8 || !ww.Slack {
		t.Errorf("the wake word's row %+v, want slot 1 at 0.9 judged against 0.8 with the allowance", ww)
	}
	if up.Slot != VolumeUpSlot+1 || up.Role != "Volume up word" || up.Threshold != 0.75 || up.Slack {
		t.Errorf("the volume up word's row %+v", up)
	}
}

// A page polls while the engine scores. The two take their locks one after the other and never one
// inside the other, so a read never deadlocks with judge. Watching from the start makes every score go
// through the board's lock, and the scoring runs for as long as the reading does, so the two overlap.
func TestScoresCanBeReadWhileTheEngineScores(t *testing.T) {
	e, d := scoresRig(t)
	d.Scores(0, 0, true)
	readDone, scoreDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(scoreDone)
		for i := 0; ; i++ {
			select {
			case <-readDone:
				return
			default:
			}
			judged(e, float64(i%100)/100, time.Now())
			runtime.Gosched()
		}
	}()
	go func() {
		defer close(readDone)
		var after uint64
		for range 500 {
			sc := d.Scores(after, 0, true)
			for _, ev := range sc.Events {
				after = max(after, ev.Seq)
			}
			runtime.Gosched()
		}
	}()
	select {
	case <-scoreDone:
	case <-time.After(20 * time.Second):
		t.Fatal("reading the scores while the engine scored did not finish")
	}
}
