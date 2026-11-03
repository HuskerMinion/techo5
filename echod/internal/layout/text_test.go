package layout

import "testing"

// Every board's own text size is one the screen can draw.
func TestDefaultTextSize(t *testing.T) {
	if v := DefaultTextSize(); v < 50 || v > 200 {
		t.Errorf("DefaultTextSize() = %d", v)
	}
}

// A board's own text size is the one the screen starts at.
func TestABoardSetsItsTextSize(t *testing.T) {
	defer func(v int) { defaultTextSize = v }(defaultTextSize)
	defaultTextSize = 80
	if v := DefaultTextSize(); v != 80 {
		t.Errorf("DefaultTextSize() = %d, want the board's 80", v)
	}
}
