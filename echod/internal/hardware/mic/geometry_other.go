//go:build !(dot && cannoli)

package mic

import "github.com/HuskerMinion/techo5/echod/internal/lib/array"

// arrayGeometry is unmeasured on this board, which keeps the delay and sum over the seven-microphone
// ring (beam.go) and its facing.
var arrayGeometry array.Geometry
