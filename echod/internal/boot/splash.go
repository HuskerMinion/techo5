package boot

import (
	"context"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

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
			err := led.Splash(actx, r, ready, setup)
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
