package config

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// A text size saved on a board that offers more (150% on a Skylight) comes back as the most this board
// offers; one within it comes back as it was.
func TestARestoredTextSizeIsHeldToTheBoards(t *testing.T) {
	for saved, want := range map[int]int{150: min(150, layout.MostTextSize()), 110: 110, 0: 0} {
		from := Defaults()
		from.Screen.TextSize = saved
		file, err := Export(from, "Echo Show 15 (skylight)", "v0")
		if err != nil {
			t.Fatal(err)
		}
		r, err := Restored(Defaults(), file, "Echo Show 8 2nd gen (athena)")
		if err != nil {
			t.Fatal(err)
		}
		if got := r.Config.Screen.TextSize; got != want {
			t.Errorf("saved %d%%, restored %d%%, want %d%%", saved, got, want)
		}
	}
}
