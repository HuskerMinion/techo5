package layout

import "testing"

// Every board's own text size is one the screen can draw.
func TestDefaultTextSize(t *testing.T) {
	if v := DefaultTextSize(); v < 50 || v > 200 {
		t.Errorf("DefaultTextSize() = %d", v)
	}
}
