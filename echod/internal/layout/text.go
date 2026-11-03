package layout

// DefaultTextSize is how large the screen draws its words until the owner picks a text size, in percent
// of the size its layout gives them. Every board draws them at that size; a board whose panel makes the
// layout's text larger on the glass than it reads well says otherwise in defaultTextSize.
func DefaultTextSize() int { return defaultTextSize }

// defaultTextSize is DefaultTextSize: 100 here, and set by a board of this build that wants another.
var defaultTextSize = 100
