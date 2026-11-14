//go:build !dot

package display

import "github.com/HuskerMinion/techo5/echod/internal/feature/voice"

// A request that came to nothing says why, in one line where the answer would have been
// (voice.State.Failed). It used to end in a chime and the clock: a device turned down, or with its
// chime off, gave no sign that anything had been asked. The line lingers as an answer does, and a tap
// puts it away as it does an answer.

// failedShown is the reason the screen gives for a failed request. The conversation cannot tell a Home
// Assistant that is away from one with no assistant for the device; the screen can, by whether Home
// Assistant is connected (linkHome).
func failedShown(why string) string {
	if why != voice.FailedNotReady {
		return why
	}
	if linkHome() {
		return voice.FailedNoAssistant
	}
	return voice.FailedNoHome
}

// failedLine is the line for a reason failedShown gave; empty for none. It names the kind of failure
// and nothing of the error behind it, which can carry an address.
func failedLine(why string) string {
	switch why {
	case "":
		return ""
	case voice.FailedNoHome:
		return tr("voice_no_home")
	case voice.FailedNoAssistant:
		return tr("voice_no_assistant")
	case voice.FailedTimeout:
		return tr("voice_timeout")
	}
	return tr("voice_failed")
}

// cueFailed is cueTurn for a turn that may have failed. A failure's line is shown under either cue,
// Cue light too: the light is out by then and has nothing to say it with. What was heard is still left
// out there, as whoever chose the light alone asked.
func cueFailed(phase, heard, reply, failed string, captions bool) (cue, page, h, r string) {
	if failed == "" || captions {
		return cueTurn(phase, heard, reply, captions)
	}
	return phase, "idle", "", ""
}
