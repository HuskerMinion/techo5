//go:build !dot

package display

import "github.com/HuskerMinion/techo5/echod/internal/feature/home"

// musicEndWord is what the now-playing page's way out says. Over this device's own music it ends the
// music, and says Stop: it said Done, which reads as putting the page away. Over a player in another
// room that the page is only following, it puts the page away until the next song and stops nothing,
// and Done is the word for that.
func musicEndWord() string {
	if musicFollowed() {
		return tr("done")
	}
	return tr("stop")
}

// musicFollowed is whether the page shows another room's player; a variable for the tests.
var musicFollowed = home.Following
