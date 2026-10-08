//go:build !dot

package display

import (
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// transport is a play, pause, back or forward button on the music: the followed player's when the page
// is showing it (home/follow.go), this device's music otherwise.
func transport(t media.Transport) {
	if home.Following() {
		go home.Get().FollowTransport(t)
		return
	}
	media.Get().Transport(t)
}
