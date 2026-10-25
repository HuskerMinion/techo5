//go:build panel

package media

import (
	"strconv"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/panelctl"
)

// The tablet's own volume buttons, or anything else that moved Android's media volume, come back as
// "volume <step>" and are taken the way Home Assistant's slider is, so Home Assistant sees the change.
func init() {
	panelctl.Handle("volume", func(args []string) {
		if len(args) == 0 {
			return
		}
		step, err := strconv.Atoi(args[0])
		if err != nil {
			return
		}
		told, at, raised := speaker.AndroidLevel()
		if crossed(at, time.Now()) {
			return
		}
		if step = fromAndroid(Get().Volume(), step, told, raised); step != Get().Volume() {
			Get().Set(step)
		}
	})
}

// fromAndroid is the media step a level Android reports stands for. Android reports where its level
// is, and while a ring raised it above the music (raised, to told) that is the ring's level, so a press
// moves the music from where it was by as much as it moved Android: music at 3 under a ring at 25,
// a press down is 24 from Android and 2 for the music.
func fromAndroid(music, reported, told int, raised bool) int {
	if !raised {
		return reported
	}
	return max(0, min(music+reported-told, VolumeSteps))
}

// androidSettle is how long after the daemon sent Android a new level a level Android reports is not
// taken. The app works a press out against the level it had; one made just as a ring's raise or drop
// went out is reported against the level before, and taken against the new one it would move the
// music to the wrong place (music at 3, a press up reported as 4 just as a raise to 25 goes out: 4
// from a ring at 25 is music at 0). Short enough that a press after it is never missed.
const androidSettle = 150 * time.Millisecond

// crossed is whether a level Android reports at now may have crossed the one the daemon sent at sent.
func crossed(sent, now time.Time) bool {
	return !sent.IsZero() && now.Sub(sent) < androidSettle
}
