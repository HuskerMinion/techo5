//go:build spot

package display

// hasCallScreen is false on the Spot: a call keeps its own page there (callscreen.go).
const hasCallScreen = false

func callScreenOptions() []string { return nil }

func callScreenIndex() int { return 0 }

func (d *Display) chooseCallScreen(int) {}
