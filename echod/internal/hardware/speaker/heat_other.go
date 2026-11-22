//go:build !dot || !cannoli

package speaker

import "context"

// watchHeat is the Echo Dot 5th gen's (heat_cannoli.go). No other tuning has a thermal table for its
// limiters, so elsewhere the speaker stays at state 0.
func (p *Player) watchHeat(context.Context) {}
