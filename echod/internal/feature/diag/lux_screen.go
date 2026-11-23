//go:build !dot

package diag

import (
	"context"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/ambient"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"
)

// luxStale is how old the light sensor's last reading may be and still be reported once its reader has
// stopped. While it is being read, a reading of any age stands: the sensor reports only when the light
// changes, and a room that stays dark sends nothing (techo5-dot#54), which left the last reading of
// the evening up all night.
const luxStale = time.Minute

// luxLongest is the longest a reading stands while the sensor is being read: the sensor reports only
// on a change, so a steady room's reading is good for hours, but one that has said nothing all day is
// more likely stuck than steady, and the file is asked instead.
const luxLongest = 12 * time.Hour

// roomLux is how bright the room is. The Show's and the Spot's sensor reports through the hwmsensor
// input device the screen's auto-brightness already reads (hardware/ambient), not through a file
// metrics.LuxPath can find; a board that has such a file is read from it instead.
func roomLux(r metrics.Reader, path string) metrics.Reading {
	if lux, at, ok := ambient.Get().Current(); ok && (ambient.Get().Live() && time.Since(at) < luxLongest || time.Since(at) < luxStale) {
		return metrics.Reading{Value: lux, Known: true}
	}
	return r.Lux(path)
}

// followLux sends the room's lux to Home Assistant as the light changes, rather than only with the
// other diagnostics every Interval, five minutes by default (techo5-dot#54): luxGap apart at least,
// and only for a change of luxStep or more.
func (d *Diag) followLux(ctx context.Context) {
	if d.lux == nil {
		return
	}
	changed := make(chan struct{}, 1)
	stop := ambient.Get().Lux.Listen(func(float64) {
		select { // the sensor's reader must not wait on this
		case changed <- struct{}{}:
		default:
		}
	})
	defer stop()
	var sent time.Time
	var between time.Duration // between the last two readings sent (luxGap)
	for {
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
		if wait := luxGap(between) - time.Since(sent); wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
		lux, _, ok := ambient.Get().Current()
		if !ok || !luxWorthSending(float64(d.lux.Get()), lux) {
			continue
		}
		d.lux.Set(float32(lux))
		if !sent.IsZero() {
			between = time.Since(sent)
		}
		sent = time.Now()
	}
}
