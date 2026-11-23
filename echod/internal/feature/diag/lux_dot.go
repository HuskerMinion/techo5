//go:build dot

package diag

import (
	"context"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"
)

// roomLux is how bright the room is: the Dot's sensor is a file metrics.LuxPath finds.
func roomLux(r metrics.Reader, path string) metrics.Reading { return r.Lux(path) }

// followLux is the screen devices' own: the Dot's sensor is a file, read with the diagnostics.
func (d *Diag) followLux(context.Context) {}
