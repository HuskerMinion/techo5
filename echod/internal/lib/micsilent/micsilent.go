// Package micsilent says whether the microphone has gone silent under the daemon: a panel's app hands
// it nothing but zeros when Android takes the microphone away (its privacy switch, another app, a
// background limit), and then neither the wake word nor anything else can be heard. The capture sets
// it (hardware/mic, capture_panel.go); Home Assistant reads it (feature/microphone). An Echo's
// microphone is its own and never sets it.
package micsilent

import (
	"sync/atomic"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
)

var silent atomic.Bool

// Changed fires with the new state each time it changes. Listeners must not block.
var Changed hook.Hook[bool]

// Set records whether the microphone is silent now, and tells the listeners if that changed.
func Set(on bool) {
	if silent.Swap(on) != on {
		Changed.Emit(on)
	}
}

// Silent is whether the microphone is silent now.
func Silent() bool { return silent.Load() }
