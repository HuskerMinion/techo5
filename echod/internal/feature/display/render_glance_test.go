//go:build !dot && !spot

package display

import (
	"slices"
	"testing"
)

func TestFitChipsSkipsOnlyTheOneThatDoesNotFit(t *testing.T) {
	for _, c := range []struct {
		name      string
		widths    []int
		room      int
		keep      []int
		wantTotal int
	}{
		{"all fit", []int{200, 200}, 744, []int{0, 1}, 412},
		{"a long one in the middle is skipped, the next still shows", []int{300, 900, 200}, 744, []int{0, 2}, 512},
		{"a long first one does not end the strip", []int{900, 200, 200}, 744, []int{1, 2}, 412},
		{"nothing fits", []int{900}, 744, nil, 0},
	} {
		keep, total := fitChips(c.widths, 12, c.room)
		if !slices.Equal(keep, c.keep) || total != c.wantTotal {
			t.Errorf("%s: kept %v (%d wide), want %v (%d)", c.name, keep, total, c.keep, c.wantTotal)
		}
	}
}
