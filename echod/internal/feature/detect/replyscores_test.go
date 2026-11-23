package detect

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

func replyLines(f *fakePeaks, stops *bool, dropped *uint64) (*replyScores, *[]string) {
	var lines []string
	r := &replyScores{marks: f, dropped: func() uint64 { return *dropped }, stops: func() bool { return *stops },
		say: func(msg string, args ...any) { lines = append(lines, msg+" "+fmt.Sprint(args...)) }}
	return r, &lines
}

// A reply's audio starting starts the scores over, and the turn ending says the stop word's peak in
// one line: when in the reply it came, what it was judged against, whether the canceller ran, whether
// the word could have fired and was heard, how long the scores were kept and how the turn ended. The
// other words follow as a call's line has them.
func TestTheStopWordsPeakIsSaidAsAReplyEnds(t *testing.T) {
	at := time.Date(2026, 11, 20, 20, 0, 0, 0, time.UTC)
	f := &fakePeaks{}
	stops, dropped := true, uint64(4)
	r, lines := replyLines(f, &stops, &dropped)

	r.follow(voice.Reply{Began: at})
	if f.marks != 1 || len(*lines) != 0 {
		t.Fatalf("marks = %d, lines = %d as the reply began, want 1 and none", f.marks, len(*lines))
	}
	dropped = 6
	f.peaks = []Peak{
		{Slot: 0, ID: "okay_nabu", Peak: 0.124, At: at.Add(3400 * time.Millisecond), Cutoff: 0.75, Canceling: true},
		{Slot: StopSlot, ID: "stop", Peak: 0.4149, At: at.Add(5240 * time.Millisecond), Cutoff: 0.5, Canceling: true},
	}
	r.follow(voice.Reply{Began: at, Ended: at.Add(20490 * time.Millisecond), Why: "spoken"})
	if len(*lines) != 1 {
		t.Fatalf("said %d lines as the reply ended, want 1", len(*lines))
	}
	want := "stop word over the reply " + fmt.Sprint("peak", 0.41, "at_s", 5.2, "cutoff", 0.5, "canceling", true,
		"held", false, "heard", false, "window_s", 20.5, "ended", "spoken", "frames_dropped", uint64(2),
		"slot1_okay_nabu", "0.12@3s")
	if (*lines)[0] != want {
		t.Errorf("said %q, want %q", (*lines)[0], want)
	}

	// The turn's end told again, or with no reply begun, says nothing.
	r.follow(voice.Reply{Began: at, Ended: at.Add(21 * time.Second), Why: "spoken"})
	if len(*lines) != 1 {
		t.Error("said the scores again with no reply")
	}
}

// A stop word that was heard says so, one held back says so, and one that was listened for and never
// scored has a line of its own; with the stop word off there is no line.
func TestAReplysLineSaysHeardHeldAndNotScored(t *testing.T) {
	at := time.Date(2026, 11, 20, 20, 0, 0, 0, time.UTC)
	f := &fakePeaks{}
	stops, dropped := true, uint64(0)
	r, lines := replyLines(f, &stops, &dropped)

	r.follow(voice.Reply{Began: at})
	f.peaks = []Peak{{Slot: StopSlot, ID: "stop", Peak: 0.58, At: at.Add(2 * time.Second), Cutoff: 0.5, Canceling: true, Fired: true}}
	r.follow(voice.Reply{Began: at, Ended: at.Add(2100 * time.Millisecond), Why: "canceled"})
	want := "stop word over the reply " + fmt.Sprint("peak", 0.58, "at_s", 2.0, "cutoff", 0.5, "canceling", true,
		"held", false, "heard", true, "window_s", 2.1, "ended", "canceled", "frames_dropped", uint64(0))
	if len(*lines) != 1 || (*lines)[0] != want {
		t.Fatalf("said %q, want %q", *lines, want)
	}

	// Over the cutoff and not heard: held back, after an unmute.
	r.follow(voice.Reply{Began: at})
	f.peaks = []Peak{{Slot: StopSlot, ID: "stop", Peak: 0.8, At: at.Add(time.Second), Cutoff: 0.5, Held: true}}
	r.follow(voice.Reply{Began: at, Ended: at.Add(3 * time.Second), Why: "spoken"})
	want = "stop word over the reply " + fmt.Sprint("peak", 0.8, "at_s", 1.0, "cutoff", 0.5, "canceling", false,
		"held", true, "heard", false, "window_s", 3.0, "ended", "spoken", "frames_dropped", uint64(0))
	if len(*lines) != 2 || (*lines)[1] != want {
		t.Fatalf("said %q, want %q", *lines, want)
	}

	r.follow(voice.Reply{Began: at})
	f.peaks = nil
	r.follow(voice.Reply{Began: at, Ended: at.Add(time.Second), Why: "cut off"})
	want = "stop word not scored over the reply " + fmt.Sprint("window_s", 1.0, "ended", "cut off", "frames_dropped", uint64(0))
	if len(*lines) != 3 || (*lines)[2] != want {
		t.Fatalf("said %q, want %q", *lines, want)
	}

	stops = false
	r.follow(voice.Reply{Began: at})
	r.follow(voice.Reply{Began: at, Ended: at.Add(time.Second), Why: "spoken"})
	if len(*lines) != 3 {
		t.Errorf("said a line with the stop word off: %q", (*lines)[3:])
	}
	if f.marks != 4 {
		t.Errorf("marks = %d, want one for each reply", f.marks)
	}
}

// A second start before an end starts the scores over, and still says one line.
func TestAReplySpokenAgainStartsTheScoresOver(t *testing.T) {
	at := time.Date(2026, 11, 20, 20, 0, 0, 0, time.UTC)
	f := &fakePeaks{}
	stops, dropped := true, uint64(0)
	r, lines := replyLines(f, &stops, &dropped)

	r.follow(voice.Reply{Began: at})
	r.follow(voice.Reply{Began: at.Add(2 * time.Second)})
	if f.marks != 2 {
		t.Fatalf("marks = %d, want the scores started over with the second audio", f.marks)
	}
	f.peaks = []Peak{{Slot: StopSlot, ID: "stop", Peak: 0.2, At: at.Add(3 * time.Second), Cutoff: 0.5}}
	r.follow(voice.Reply{Began: at.Add(2 * time.Second), Ended: at.Add(6 * time.Second), Why: "spoken"})
	want := "stop word over the reply " + fmt.Sprint("peak", 0.2, "at_s", 1.0, "cutoff", 0.5, "canceling", false,
		"held", false, "heard", false, "window_s", 4.0, "ended", "spoken", "frames_dropped", uint64(0))
	if len(*lines) != 1 || (*lines)[0] != want {
		t.Fatalf("said %q, want %q", *lines, want)
	}
}

// What the device runs: voice tells of a reply, the engine's own keeper keeps the stop word's scores
// over it, and the line is said as the turn ends. Scores before the reply, and after it, are not its.
func TestARepliesStopWordScoresReachTheLog(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	made := fakes(t)
	e := New(StopSlot+1, nil)
	e.Threshold = func(int) float64 { return 0.5 }
	if err := e.Use(StopSlot, model("stop", wake.KindMicroWakeWord)); err != nil {
		t.Fatalf("Use(StopSlot): %v", err)
	}
	scores := made[wake.KindMicroWakeWord].loaded

	var lines []string
	t.Cleanup(followReplies(e, func(msg string, args ...any) { lines = append(lines, msg+" "+fmt.Sprint(args...)) }))

	scores["stop"] = 0.45
	e.score([]int16{0}, nil)

	began := time.Now()
	voice.Replying.Emit(voice.Reply{Began: began})
	scores["stop"] = 0.31
	e.score([]int16{0}, nil)
	scores["stop"] = 0.12
	e.score([]int16{0}, nil)
	voice.Replying.Emit(voice.Reply{Began: began, Ended: began.Add(4 * time.Second), Why: "spoken"})

	scores["stop"] = 0.4
	e.score([]int16{0}, nil)

	if len(lines) != 1 {
		t.Fatalf("said %q, want one line", lines)
	}
	if want := "stop word over the reply " + fmt.Sprint("peak", 0.31); !strings.HasPrefix(lines[0], want) {
		t.Errorf("said %q, want the stop word's 0.31 from the reply alone", lines[0])
	}
}

// A reply that played on another speaker says so, since its scores are over the room, not over a reply.
func TestAReplyOnAnotherSpeakerSaysSo(t *testing.T) {
	at := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	f := &fakePeaks{}
	stops, dropped := true, uint64(0)
	r, lines := replyLines(f, &stops, &dropped)
	r.follow(voice.Reply{Began: at})
	f.peaks = []Peak{{Slot: StopSlot, ID: "stop", Peak: 0.2, At: at.Add(time.Second), Cutoff: 0.5}}
	r.follow(voice.Reply{Began: at, Ended: at.Add(5 * time.Second), Why: "spoken", Elsewhere: true})
	if len(*lines) != 1 || !strings.Contains((*lines)[0], "elsewheretrue") {
		t.Fatalf("said %q, want the line to say elsewhere", *lines)
	}
}
