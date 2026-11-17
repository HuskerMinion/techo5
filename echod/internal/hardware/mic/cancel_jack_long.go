//go:build dot || spot

package mic

// longJack is whether the headphone jack gets cancelTapsJack. On the Dot and the Spot the jack drives
// a speaker across the room, the path cancelTapsJack was measured on.
const longJack = true
