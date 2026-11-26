//go:build !dot && !spot

package display

import (
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
)

// muteLiftedFor is how long the card stays: it explains what just happened at the device, to whoever
// pressed the button, and is not news to come back to.
const muteLiftedFor = 3 * time.Second

// muteLiftedNote is the card that says the mute button took a mute set from Home Assistant or the
// screen with it (feature/mute's LiftedByButton). A note of the device's own: silent, short, and never
// over a notification Home Assistant sent.
func muteLiftedNote() {
	notify.Get().NoteFor(tr("microphone"), tr("mute_lifted_by_button"), muteLiftedFor)
}
