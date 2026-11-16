package detect

import (
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
)

// callScores puts in the log, as each call ends, the most each word scored during it and how many
// microphone frames went missing. A word said during a call that left no wake, near miss or hang-up
// line behind (#484) then shows whether it was heard at all: a peak far under the near miss is a word
// the microphones or the echo canceller lost, and frames missing is a loop that could not keep up.
type callScores struct {
	mu      sync.Mutex
	marks   peakMarker
	dropped func() uint64
	say     func(msg string, args ...any)

	up      bool      // a call is up and its scores are being kept
	since   time.Time // when it came up
	missing uint64    // frames missed before it did
}

// peakMarker is the engine's side of it, so a test can stand in.
type peakMarker interface {
	MarkPeaks()
	Peaks() []Peak
}

// follow is a change of the phone's state: a call coming up starts the scores over, and one ending
// says them.
func (c *callScores) follow(st phone.State, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	talking := st.Phase == phone.Talking
	switch {
	case talking && !c.up:
		c.up, c.since, c.missing = true, now, c.dropped()
		c.marks.MarkPeaks()
	case !talking && c.up:
		c.up = false
		args := []any{"after", now.Sub(c.since).Round(time.Second), "frames_dropped", c.dropped() - c.missing}
		for _, p := range c.marks.Peaks() {
			args = append(args, fmt.Sprintf("slot%d_%s", p.Slot+1, p.ID),
				fmt.Sprintf("%.2f@%ds", math.Round(p.Peak*100)/100, int(p.At.Sub(c.since).Seconds())))
		}
		c.say("wake scores during the call", args...)
	}
}

func newCallScores(e *Engine) *callScores {
	return &callScores{
		marks:   e,
		dropped: e.source.Dropped,
		say:     slog.Info,
	}
}
