package layout

// DefaultTextSize is how large the screen draws its words until the owner picks a text size, in percent
// of the size its layout gives them. Every board draws them at that size; a board whose panel makes the
// layout's text larger on the glass than it reads well says otherwise here.
func DefaultTextSize() int { return 100 }
