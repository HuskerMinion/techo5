//go:build !dot

package person

import (
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/tflite"
)

// An accelerator runs the model somewhere other than the cores: a board's NPU or VPU. A board that has
// one registers its opener from a file of its own (an init); the first that opens one for the model
// is used, and the cores take over for good when it fails. With none registered, the cores do it all.
type accelerator interface {
	// score is the chance a 96x96 gray picture shows a person; false if it failed, and it is not to be
	// used again.
	score(gray []uint8, now time.Time) (float32, bool)
	// idle lets the hardware go when it has not been used for a while.
	idle(now time.Time)
	close()
}

// accelerators open an accelerator for the loaded model, nil where the board has none for it.
var accelerators []func(m *tflite.Model, in *tflite.Interpreter) accelerator

func openAccel(m *tflite.Model, in *tflite.Interpreter) accelerator {
	for _, open := range accelerators {
		if a := open(m, in); a != nil {
			return a
		}
	}
	return nil
}

// idle lets an accelerator go when it has not been used for a while.
func (d *detector) idle(now time.Time) {
	if d.acc != nil {
		d.acc.idle(now)
	}
}

// close lets an accelerator go.
func (d *detector) close() {
	if d.acc != nil {
		d.acc.close()
	}
}
