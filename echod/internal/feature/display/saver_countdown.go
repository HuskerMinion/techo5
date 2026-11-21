//go:build !dot

package display

import "time"

// countdownStep is how often the screensaver's countdown is drawn again (#656): as often as it moves
// about two pixels of the span pixels it fills over a photo's whole time, so it fills smoothly rather
// than in a jump each second, but no faster than countdownFastest, since each of those frames draws the
// whole screen, and no slower than once a second, as the clock is.
func countdownStep(whole time.Duration, span int) time.Duration {
	if whole <= 0 || span <= 0 {
		return time.Second
	}
	return min(max(whole*2/time.Duration(span), countdownFastest), time.Second)
}

// countdownFastest is the countdown's quickest redraw: four a second. Each is the whole screen, and on
// the oldest Shows ten a second was steady work beside the wake word for a line a few pixels long.
const countdownFastest = 250 * time.Millisecond
