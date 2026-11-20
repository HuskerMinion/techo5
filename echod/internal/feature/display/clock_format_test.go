//go:build !dot

package display

import "testing"

// Clock format's three choices map to the two settings and back, so the select and the screen's picker
// show what was chosen.
func TestClockFormatChoices(t *testing.T) {
	defer setClockFormat(nil, false, false)
	for i, label := range clockLabels {
		h24, bare := clockFormatOf(label)
		setClockFormat(nil, h24, bare)
		if got := clockIndex(); got != i {
			t.Errorf("%q: clockIndex = %d, want %d", label, got, i)
		}
		if optionLabel(label) == "" {
			t.Errorf("%q has no label on the screen", label)
		}
	}
	if h24, bare := clockFormatOf(clock12BareLabel); h24 || !bare {
		t.Errorf("12-hour, no AM/PM is h24=%v bare=%v", h24, bare)
	}
}
