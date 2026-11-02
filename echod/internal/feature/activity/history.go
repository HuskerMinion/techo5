package activity

import (
	"strconv"
	"sync"
	"time"
)

// The last few voice turns, for the setup page. They live in memory only: nothing here is written to
// disk, put in a settings file or the diagnostics download, or sent anywhere, and a restart empties it.
// A turn is added from the place its event is sent, from the same fields, so the two always agree, and
// the words follow Send voice text exactly as the event does.

// kept is how many turns the history holds.
const kept = 10

// Past is one finished turn as the setup page shows it.
type Past struct {
	At    time.Time
	Slot  int
	Score string // the wake word's peak, empty when the device's own engine did not open the turn
	Heard string
	Reply string
	// Withheld says the words were left out because Send voice text is off.
	Withheld bool
	// End is how it ended: stopped, interrupted, cut_off, answered_elsewhere or completed, and for the
	// turns that were neither, no_speech or error.
	End string
	// How long each part took in milliseconds, empty for one the turn never reached.
	ListenMS, ThinkMS, ReplyMS string
}

var (
	pastMu sync.Mutex
	past   []Past // oldest first, at most kept
)

// remember adds a turn, from the fields of the event that reports it.
func remember(at time.Time, f map[string]string, withheld bool) {
	slot, _ := strconv.Atoi(f["slot"])
	end := f["ended"]
	switch {
	case f["reason"] != "":
		end = f["reason"]
	case end == string(Answered):
		end = "completed"
	}
	p := Past{
		At: at, Slot: slot, Score: f["wake_score"], Heard: f["heard"], Reply: f["reply"], Withheld: withheld,
		End: end, ListenMS: f["listen_ms"], ThinkMS: f["think_ms"], ReplyMS: f["reply_ms"],
	}
	pastMu.Lock()
	defer pastMu.Unlock()
	past = append(past, p)
	if len(past) > kept {
		past = append(past[:0], past[len(past)-kept:]...)
	}
}

// History is the last turns, newest first.
func History() []Past {
	pastMu.Lock()
	defer pastMu.Unlock()
	out := make([]Past, len(past))
	for i, p := range past {
		out[len(past)-1-i] = p
	}
	return out
}

// ClearHistory forgets them.
func ClearHistory() {
	pastMu.Lock()
	past = nil
	pastMu.Unlock()
}
