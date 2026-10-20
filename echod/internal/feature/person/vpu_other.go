//go:build !linux && !dot

package person

import (
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/tflite"
)

// accel is the model on the VPU (vpu.go), which only a Linux board has.
type accel struct{}

func newAccel(*tflite.Model, *tflite.Interpreter) *accel { return nil }

func (*accel) score([]uint8, time.Time) (float32, bool) { return 0, false }
func (*accel) idle(time.Time)                           {}
func (*accel) close()                                   {}
