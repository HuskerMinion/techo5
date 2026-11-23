package detect

import (
	"fmt"
	"math"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
)

// replyScores puts in the log, as each reply ends, the most the stop word scored while it was said: how
// far into the reply that was, what it was judged against, whether the echo canceller was running, and
// whether the word was heard. A "stop" said over a reply that left no wake or near miss line behind then
// shows how far short it fell. A peak close under the cutoff is a word that nearly made it; one far under
// it is a word the canceller, or the reply itself, took from the microphones. While the speaker plays,
// the stop word's cutoff is often already as low as it goes (thresholdFor), so a lower threshold may not
// bring it down: what the line is for is telling those two apart, and comparing the peaks before and
// after a change to what the stop word hears.
//
// It is one number a reply, the highest. Several tries over one reply show as the best of them, and a
// reply nobody spoke over shows what the reply alone made the stop word score, which is what a try has to
// be told apart from. Measuring it is one "stop" a reply, and some replies with none.
//
// The window is from the reply's first audio arriving to the turn's end, which is a little longer than
// the reply sounded: a streamed reply waits for its cushion before it plays, and the canceller only
// starts once the speaker does. A peak in that first moment is judged as in a quiet room, and says so
// (canceling=false, the cutoff without the slack for playing). A reply played on another media player
// is told too, though nothing sounded here (canceling=false again), and so is one sent into a call,
// which the room did not hear. Nothing is kept across a restart, so a reply the daemon restarted under
// has no line. A restart of the engine (Start), or of the echo canceller, under a reply leaves its
// scores as they were: the line then has what the stop word scored before it beside what it scored
// after, with the word loaded afresh or the canceller learning again.
//
// The other words' peaks follow as a call's line has them (callscores.go). What a reply by itself scores
// on a wake word is what that word's threshold for playing has to stay over.
type replyScores struct {
	marks   peakMarker
	dropped func() uint64
	// stops is whether the stop word is listened for at all: with it off there is nothing to say.
	stops func() bool
	say   func(msg string, args ...any)

	// Only the conversation's goroutine calls follow, so these need no lock.
	began   time.Time // when the reply being said began, zero with none
	missing uint64    // frames missed before it did
}

// follow is a reply's audio starting, or the turn it answers ending (voice.Replying). It runs on the
// conversation's goroutine, which the keeper never holds up (PeakKeeper). A reply whose end is never told
// leaves its keeper marking until the next reply starts it over, a lock and a compare a scored frame
// meanwhile.
func (r *replyScores) follow(v voice.Reply) {
	if v.Ended.IsZero() {
		// voice tells one start a turn, a reply taken over by its url included. A second start with no
		// end between still starts the scores over, so a lost end never runs one reply into the next.
		r.began, r.missing = v.Began, r.dropped()
		r.marks.MarkPeaks()
		return
	}
	if r.began.IsZero() {
		return
	}
	began := r.began
	r.began = time.Time{}
	peaks := r.marks.Peaks()
	if !r.stops() {
		return
	}

	// window_s is the time the scores were kept over, from the first audio arriving to the turn's end:
	// not how long the reply sounded, which "reply played" has as seconds.
	args := []any{"window_s", tenths(v.Ended.Sub(began)), "ended", v.Why, "frames_dropped", r.dropped() - r.missing}
	if v.Elsewhere {
		// The reply played on another speaker: nothing was playing here for the canceller to take out,
		// so these are the stop word's scores over the room, not over a reply.
		args = append(args, "elsewhere", true)
	}
	var others []any
	scored := false
	for _, p := range peaks {
		if p.Slot != StopSlot {
			others = append(others, fmt.Sprintf("slot%d_%s", p.Slot+1, p.ID),
				fmt.Sprintf("%.2f@%ds", hundredths(p.Peak), int(p.At.Sub(began).Seconds())))
			continue
		}
		scored = true
		args = append([]any{"peak", hundredths(p.Peak), "at_s", tenths(p.At.Sub(began)), "cutoff", hundredths(p.Cutoff),
			"canceling", p.Canceling, "held", p.Held, "heard", p.Fired}, args...)
	}
	if !scored {
		// Listened for and never scored: the microphones were cut, or the loop was not running.
		r.say("stop word not scored over the reply", append(args, others...)...)
		return
	}
	r.say("stop word over the reply", append(args, others...)...)
}

// tenths is d in seconds, to a tenth; hundredths is a score to two places, as the scores view has it.
func tenths(d time.Duration) float64 { return math.Round(d.Seconds()*10) / 10 }
func hundredths(v float64) float64   { return math.Round(v*100) / 100 }

// followReplies keeps the engine's scores over each reply, from a keeper of their own, and says them
// with say as the reply ends. It returns what stops it.
func followReplies(e *Engine, say func(msg string, args ...any)) func() {
	r := &replyScores{
		marks:   e.NewPeaks(),
		dropped: e.source.Dropped,
		stops:   func() bool { return config.Get().Wake.Stop.Listening() },
		say:     say,
	}
	return voice.Replying.Listen(r.follow)
}
