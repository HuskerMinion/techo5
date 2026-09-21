//go:build spot

package announce

import "time"

// shows is how long an announcement stays on the screen.
//
// Shorter here than on the Show, because a circle has no corner to put it in: it takes the whole
// face, and everything else on the device is behind it until it goes. Forty-five seconds of that is
// a long wait for something that finished being said in three.
const shows = 20 * time.Second
