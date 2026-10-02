//go:build !dot

package display

import (
	"context"
	"time"
)

// settleEvery is how often the backlight steps toward the room's level between readings. The light
// sensor reports a change, not every period: the input layer drops a value equal to the last, so a
// room that has stopped changing sends nothing. A backlight that moved only on a reading was left part
// of the way there until something else relit the screen.
const settleEvery = 500 * time.Millisecond

// settle keeps auto-brightness moving until ctx is canceled.
func (d *Display) settle(ctx context.Context) {
	t := time.NewTicker(settleEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.settleStep()
		}
	}
}

// settleStep takes one smoothed step toward the room's level, if the backlight is not there yet.
func (d *Display) settleStep() {
	d.mu.Lock()
	due := d.autoOn && d.on && !d.settled
	d.mu.Unlock()
	if due {
		d.relight(false)
	}
}
