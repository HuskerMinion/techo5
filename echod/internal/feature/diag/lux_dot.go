//go:build dot

package diag

import "github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"

// roomLux is how bright the room is: the Dot's sensor is a file metrics.LuxPath finds.
func roomLux(r metrics.Reader, path string) metrics.Reading { return r.Lux(path) }
