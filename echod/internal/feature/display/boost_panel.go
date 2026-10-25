//go:build panel

package display

import "time"

// A tablet's cores are Android's: an app may not set how many stay awake (diag's coresHere), so the
// panel holds none.
const boostLinger = 1500 * time.Millisecond

type boost struct{ on bool }

func (b *boost) update(bool) {}

func (b *boost) set(bool) {}
