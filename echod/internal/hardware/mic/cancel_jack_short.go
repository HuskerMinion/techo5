//go:build !dot && !spot

package mic

// longJack is whether the headphone jack gets cancelTapsJack. On a Show the jack is for earbuds or
// headphones, with next to no echo to cancel, so the four times longer filter would cost the CPU for
// nothing.
const longJack = false
