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

// Chime plays the two soft falling notes, once, at the speaker's own volume or the floor, and returns
// when they are done. The calendar pop-up uses it too, since the two are the same kind of thing.
func Chime() {
	claim := speaker.Sound().Claim("notification", func(ctx context.Context, pl *speaker.Player) error {
		pl.Bell(max(pl.Step(), chimeFloor), 0.5, tone...)
		select {
		case <-ctx.Done():
		case <-time.After(700 * time.Millisecond):
		}
		return nil
	})
	<-claim.Done()
}
