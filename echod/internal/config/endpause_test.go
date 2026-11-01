package config

import "testing"

// None is the default, and a value from an older or edited file is brought into the range.
func TestEndPauseTenths(t *testing.T) {
	for set, want := range map[int]int{0: DefaultEndPause, 3: MinEndPause, 6: 6, 12: 12, 20: 20, 45: MaxEndPause} {
		if got := (Microphone{EndPause: set}).EndPauseTenths(); got != want {
			t.Errorf("EndPause %d: %d, want %d", set, got, want)
		}
	}
}
