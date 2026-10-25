//go:build panel

package video

// Here is whether this device plays videos: a panel does not. The decoder is the techo5-ffmpeg the
// Echos' images carry, and the app has none, so the switches and the play_video actions would do
// nothing there. The package builds as the Show's, for the screen code that asks it what is playing.
const Here = false
