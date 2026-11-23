//go:build dot

package diag

import (
	"context"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"
)

// roomLux is how bright the room is: the Dot's sensor is a file metrics.LuxPath finds.
func roomLux(r metrics.Reader, path string) metrics.Reading { return r.Lux(path) }

// luxPoll is how often the Dot's lux file is read to follow the light. The file is the sensor's own
// value, read in a moment, and nothing tells the Dot when it changes, so it is looked at.
var luxPoll = 2 * time.Second

// followLux sends the room's lux to Home Assistant as the light changes, as the screen devices do,
// rather than only with the other diagnostics every five minutes: the file is read every luxPoll, and
// a change of luxStep or more goes, luxGap apart at least. A Dot with no lux file has nothing to follow.
func (d *Diag) followLux(ctx context.Context) {
	if d.lux == nil || d.luxPath == "" {
		return
	}
	t := time.NewTicker(luxPoll)
	defer t.Stop()
	var sent time.Time
	var between time.Duration // between the last two readings sent (luxGap)
	for {
		var now time.Time
		select {
		case <-ctx.Done():
			return
		case now = <-t.C:
		}
		// Counted from tick to tick, so a gap of luxPoll is met on the next tick, not the one after.
		if now.Sub(sent) < luxGap(between) {
			continue
		}
		r := roomLux(metrics.Reader{}, d.luxPath)
		if !r.Known || !luxWorthSending(float64(d.lux.Get()), r.Value) {
			continue
		}
		d.lux.Set(float32(r.Value))
		if !sent.IsZero() {
			between = now.Sub(sent)
		}
		sent = now
	}
}
