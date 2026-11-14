package voice

import "github.com/HuskerMinion/techo5/echod/internal/lib/hook"

// State is what a turn looks like from outside: which phase it is in and the words so far. It is
// what a screen shows — "Listening…", then what was heard, then the answer — and it is a hook
// rather than a query because a screen wants to redraw the moment something changes, not poll.
//
// Phase is the conversation's own name for it: idle, listening, thinking, replying. Heard and Reply
// are kept through the idle that follows a turn, so whoever shows them can let them linger.
//
// Failed is why a request came to nothing, one of the Failed constants, and empty for a turn that
// ended any other way. It comes with the idle that ends the request and with nothing after it: the
// chime that says the same thing is not heard on a device turned down, and until this the screen went
// back to the clock as it does after an answer.
type State struct {
	Phase  string
	Heard  string
	Reply  string
	Failed string
}

// Why a request failed, as far as the conversation can tell.
const (
	// FailedNoHome: what the turn had for Home Assistant could not be sent to it.
	FailedNoHome = "no_home"
	// FailedNotReady: Home Assistant has no voice pipeline listening to this device. It is away, or it
	// has no assistant for the device; from here the two look the same, and whoever shows it knows
	// which by whether Home Assistant is connected.
	FailedNotReady = "not_ready"
	// FailedNoAssistant: nothing is set up to answer: realtime voice that is not ready, or a wake
	// word's place with no wake word in it to reach its assistant by.
	FailedNoAssistant = "no_assistant"
	// FailedTimeout: nobody answered in the time a turn is given.
	FailedTimeout = "timeout"
	// FailedError: the assistant took the request and reported an error.
	FailedError = "error"
)

// failureFor is the reason behind a pipeline error's code. Only the device's own code for a send that
// failed says more than that there was one: Home Assistant's are about its side of the pipeline.
func failureFor(code string) string {
	if code == "send" {
		return FailedNoHome
	}
	return FailedError
}

// Changed fires on every phase change and whenever a transcript or a reply arrives. It runs on the
// conversation's goroutine, so listeners must not block.
var Changed hook.Hook[State]
