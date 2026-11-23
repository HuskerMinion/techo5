package voice

import (
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
)

// Reply is a reply being said: told once as its audio starts, and once more as the turn it answers
// ends, however that is.
type Reply struct {
	// Began is when its audio started arriving, which tells the two tellings of one reply to be the
	// same. It is a little before the speaker sounds it: a streamed reply is held back for its cushion
	// first (wakeword.Buffer).
	Began time.Time

	// Ended is when the turn ended, zero while the reply is still being said, and Why how, as the
	// "turn ended" line has it.
	Ended time.Time
	Why   string

	// Elsewhere is the reply having played on the chosen reply player, not this device's speaker: the
	// stop word was then listened for over a quiet room, not over a reply, and its scores say so.
	Elsewhere bool
}

// Replying is told of every reply that had audio, as that starts and as the turn ends: for what
// measures something over a reply (detect's scores of the stop word). A reply stopped before any audio
// came is not told at all, nor one whose first audio was still waiting in the loop when the turn was
// stopped. A turn that speaks a second time (a streamed reply taken over by its url) is one reply,
// begun as its first audio came: it is told one start and one end, however far the url's audio got.
// It runs on the conversation's goroutine, so listeners must not block.
var Replying hook.Hook[Reply]
