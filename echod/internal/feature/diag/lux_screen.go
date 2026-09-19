//go:build !dot

package diag

import (
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/ambient"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"
)

// luxStale is how old the light sensor's last reading may be and still be reported. It reports twice
// a second, so a minute without one means it has stopped.
const luxStale = time.Minute

// roomLux is how bright the room is. The Show's and the Spot's sensor reports through the hwmsensor
// input device the screen's auto-brightness already reads (hardware/ambient), not through a file
// metrics.LuxPath can find; a board that has such a file is read from it instead.
func roomLux(r metrics.Reader, path string) metrics.Reading {
	if lux, at, ok := ambient.Get().Current(); ok && time.Since(at) < luxStale {
		return metrics.Reading{Value: lux, Known: true}
	}
	return r.Lux(path)
}
