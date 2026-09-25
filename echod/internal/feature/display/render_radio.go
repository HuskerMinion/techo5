//go:build !dot && !spot

package display

import (
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// The radio page: what is playing, the stations Home Assistant lists, a Stop row while there is
// something to stop, and the bar that closes the page. Rows fit the 480-row panel: up to seven of 44
// from 92.
//
// radioList is the rows in order: "Stop" first while there is something to stop, then the stations. A
// paused track counts, and that is not a detail: Music Assistant ends its stream when it pauses, so a
// remote's track paused from here is held for the screen with play offered — the page says "Paused" and
// offers to play it, while this list offered nothing at all, because playing was the only thing it knew
// about. The Spot's list gates on the music's state and had it right; this one now does too.
func radioList(rd home.Radio) []string {
	var rows []string
	if rd.Playing || rd.Paused || rd.Chosen != "" {
		rows = append(rows, "■ Stop")
	}
	return append(rows, rd.Stations...)
}
