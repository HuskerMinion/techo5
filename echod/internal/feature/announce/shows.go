//go:build !spot

package announce

import "time"

// shows is how long an announcement stays on the screen.
//
// On the Show it is a strip along the bottom and the clock and the music carry on behind it, so it
// can sit there: nothing is in anybody's way. A Dot has no screen and does not care.
const shows = 45 * time.Second
