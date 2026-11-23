//go:build !dot

package diag

import (
	"context"
	"math"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/ambient"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"
)

// luxStale is how old the light sensor's last reading may be and still be reported once its reader has
// stopped. While it is being read, a reading of any age stands: the sensor reports only when the light
// changes, and a room that stays dark sends nothing (techo5-dot#54), which left the last reading of
// the evening up all night.
const luxStale = time.Minute

// luxEvery is the least time between two lux readings sent as the light changes, and luxStep the
// change worth sending: a lamp switched on or off, a cloud, not every flicker of a reading. Between
// them, the diagnostics' own interval sends it as before.
const (
	luxEvery = 2 * time.Second
	luxStep  = 0.1 // of the last reading sent, and at least 1 lux
)

// roomLux is how bright the room is. The Show's and the Spot's sensor reports through the hwmsensor
// input device the screen's auto-brightness already reads (hardware/ambient), not through a file
// metrics.LuxPath can find; a board that has such a file is read from it instead.
func roomLux(r metrics.Reader, path string) metrics.Reading {
	if lux, at, ok := ambient.Get().Current(); ok && (ambient.Get().Live() || time.Since(at) < luxStale) {
		return metrics.Reading{Value: lux, Known: true}
	}
	return r.Lux(path)
}

// followLux sends the room's lux to Home Assistant as the light changes, rather than only with the
// other diagnostics every Interval, five minutes by default (techo5-dot#54): at most once every
// luxEvery, and only for a change of luxStep or more.
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
	for {
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
		if wait := luxEvery - time.Since(sent); wait > 0 {
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
		sent = time.Now()
	}
}

// luxWorthSending is whether a reading differs enough from the one Home Assistant has to send it.
func luxWorthSending(had, now float64) bool {
	return math.Abs(now-had) >= max(1, luxStep*had)
}
