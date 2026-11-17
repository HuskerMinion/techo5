//go:build !dot && !spot && !panel

package display

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// Every Echo Show 5 and Show 8 board offers the line, the 2nd gen Show 8 on either kernel (both are the
// athena board); the Skylight does not.
func TestNightAlarmHereOnTheShow5AndShow8(t *testing.T) {
	was := layout.Board
	t.Cleanup(func() { layout.Board = was })
	for board, want := range map[string]bool{"cronos": true, "checkers": true, "crown": true, "athena": true, "skylight": false} {
		layout.Board = board
		if got := nightAlarmHere(); got != want {
			t.Errorf("%s: offered %v, want %v", board, got, want)
		}
	}
}
