package boot

import (
	"context"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// waitWalk is how long the ring walks after a start while Home Assistant has not connected: long
// enough to say "starting, not connected yet", not so long that a device used without Home Assistant
// walks for as long as it is on (techo5-dot#39).
const waitWalk = 5 * time.Minute

// walkFor is waitWalk, or none at all when the setup page's switch has left it out.
func walkFor() time.Duration {
	if config.Get().Ring.NoWaitWalk {
		return 0
	}
	return waitWalk
}

// startSplash runs the boot animation and lets go of the ring once the device can actually answer.
//
// Not a service: it finishes, and nothing waits on it or restarts it. It holds the boot claim, which
// outranks everything, so nothing has to be withheld while it runs — the light entity and the saved
// volume can arrive whenever they like and sit underneath until the claim goes away.
func startSplash(ctx context.Context, leds *led.Driver, ready, setup func() bool) {
	safe.Go("splash", func() {
		claim := leds.Claim(led.PriorityBoot)
		defer claim.Release()

		// Buffered and sent to without blocking, so finishing cannot wedge the driver.
		done := make(chan struct{}, 1)
		claim.Show(led.Content{Animate: func(actx context.Context, r *led.Ring) error {
			err := led.Splash(actx, r, ready, setup, walkFor())
			select {
			case done <- struct{}{}:
			default:
			}
			return err
		}})

		select {
		case <-done:
		case <-ctx.Done():
		}
	})
}
