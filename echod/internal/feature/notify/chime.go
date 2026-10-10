package notify

import (
	"context"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// tone is what a notification arrives with: the calendar pop-up's two notes falling, since the two are
// the same kind of thing — something to look at, nobody waiting on an answer.
var tone = []speaker.Note{{Freq: 988, Ms: 150}, {Freq: 0, Ms: 60}, {Freq: 784, Ms: 260}}

// chimeFloor is the least the chime plays at, in volume steps, so it is heard with the volume down.
const chimeFloor = 6

// playChime is the notification's chime, at the speaker's own volume or the floor. The calendar's is in
// a file built for the Show alone, so this is its own, built for every device.
func playChime() {
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
