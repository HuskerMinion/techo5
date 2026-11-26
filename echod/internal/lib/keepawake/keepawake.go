// Package keepawake is whether an add-on holds the screen awake now (feature/addonapi, awake.hold),
// which the screen reads as being in use: it does not dim after idle and does not go out for an empty
// room. A package of its own so the screen and the add-on API need not know each other.
//
// What it does not do is outrank the night or a closed cover, which decide before use is counted, and
// it ends with the hold: when the add-on lets go, its time runs out, its connection drops or it stops.
package keepawake

import "sync/atomic"

var held atomic.Bool

// Set says whether something holds the screen awake.
func Set(on bool) { held.Store(on) }

// Held is whether something holds the screen awake now.
func Held() bool { return held.Load() }
