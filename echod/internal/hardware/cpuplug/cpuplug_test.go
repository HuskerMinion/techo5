//go:build linux && !dot && !spot

package cpuplug

import "testing"

func TestBusy(t *testing.T) {
	a := stat{"cpu0": {100, 1000}, "cpu1": {0, 1000}}
	b := stat{"cpu0": {190, 1100}, "cpu1": {10, 1100}, "cpu2": {5, 10}}
	if got := busy(a, b); got != 0.5 {
		t.Fatalf("busy %v, want 0.5 (cpu2 came up between and is left out)", got)
	}
	if busy(b, b) != 0 {
		t.Fatal("no time passed, yet busy")
	}
}
