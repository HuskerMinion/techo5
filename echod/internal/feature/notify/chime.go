package notify

import (
	"context"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// tone is what a notification arrives with: two soft notes falling, the calendar pop-up's too, since the
// two are the same kind of thing: something to look at, nobody waiting on an answer.
var tone = []speaker.Note{{Freq: 988, Ms: 150}, {Freq: 0, Ms: 60}, {Freq: 784, Ms: 260}}

// chimeFloor is the least the chime plays at, in volume steps, so it is heard with the volume down.
const chimeFloor = 6

// chimeWait is how long the chime waits for a reply or an announcement to finish before it is left out:
// the notification is on the screen either way, and a chime long after it came up says nothing. A
// camera's sound is not waited for: the chime plays over it (speaker.ClaimTurn).
const chimeWait = 10 * time.Second

// Chime plays the two soft falling notes, once, at the speaker's own volume or the floor, and returns
// when they are done. The calendar pop-up uses it too, since the two are the same kind of thing. It waits
// its turn rather than cutting off what is being said (speaker.ClaimTurn), and sounds in the bell's own
// lane, which is what lets it play over a camera's sound rather than wait out the view.
func Chime() { ChimeAt(0) }

// ChimeAt is Chime at step, in volume steps (1 to VolumeSteps), for a notification sent with a volume of
// its own (notify_buttons); 0 or less is Chime's own level.
func ChimeAt(step int) {
	claim := speaker.Sound().ClaimTurn("notification", chimeWait, func(ctx context.Context, pl *speaker.Player) error {
		level := max(pl.Step(), chimeFloor)
		if step > 0 {
			level = min(step, speaker.VolumeSteps)
		}
		pl.Bell(level, 0.5, tone...)
		select {
		case <-ctx.Done():
		case <-time.After(700 * time.Millisecond):
		}
		return nil
	})
	<-claim.Done()
}
