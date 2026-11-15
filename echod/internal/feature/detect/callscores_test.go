package detect

import (
	"fmt"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
)

type fakePeaks struct {
	marks int
	peaks []Peak
}

func (f *fakePeaks) MarkPeaks()    { f.marks++ }
func (f *fakePeaks) Peaks() []Peak { return f.peaks }

// A call coming up starts the scores over once, however many changes it brings, and one ending says
// each word's peak, when in the call it came, and the frames lost meanwhile (#484).
func TestACallsScoresAreSaidAsItEnds(t *testing.T) {
	at := time.Date(2026, 11, 15, 20, 0, 0, 0, time.UTC)
	f := &fakePeaks{}
	dropped := uint64(7)
	var said []any
	var lines int
	c := &callScores{marks: f, dropped: func() uint64 { return dropped },
		say: func(_ string, args ...any) { lines++; said = args }}

	c.follow(phone.State{Phase: phone.Ringing}, at)
	if f.marks != 0 {
		t.Fatal("scores started over before the call was up")
	}
	c.follow(phone.State{Phase: phone.Talking}, at)
	c.follow(phone.State{Phase: phone.Talking}, at.Add(time.Second))
	if f.marks != 1 {
		t.Fatalf("marks = %d, want 1 for one call", f.marks)
	}
	dropped = 10
	f.peaks = []Peak{{Slot: 0, ID: "alexa", Peak: 0.314, At: at.Add(42 * time.Second)}}
	c.follow(phone.State{Phase: phone.Idle}, at.Add(71*time.Second))
	if lines != 1 {
		t.Fatalf("said %d lines as the call ended, want 1", lines)
	}
	got := fmt.Sprint(said...)
	want := fmt.Sprint("after", time.Minute+11*time.Second, "frames_dropped", uint64(3), "slot1_alexa", "0.31@42s")
	if got != want {
		t.Errorf("said %q, want %q", got, want)
	}
	c.follow(phone.State{Phase: phone.Idle}, at.Add(80*time.Second))
	if lines != 1 {
		t.Error("said the scores again with no call")
	}
}
