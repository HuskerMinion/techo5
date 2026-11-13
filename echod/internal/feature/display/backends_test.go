//go:build !dot

package display

import "testing"

// A row of a board's own is tapped through its handler, and only its own.
func TestABoardsRowTaps(t *testing.T) {
	saved := rowTaps
	t.Cleanup(func() { rowTaps = saved })
	var tapped []string
	rowTaps = []func(*Display, string, part, int) bool{
		func(_ *Display, id string, _ part, _ int) bool {
			tapped = append(tapped, "first:"+id)
			return id == "mine"
		},
		func(_ *Display, id string, _ part, _ int) bool { tapped = append(tapped, "second:"+id); return false },
	}
	d := &Display{}
	if !d.boardRowTap("mine", 0, 0) || len(tapped) != 1 {
		t.Fatalf("tapped %v", tapped)
	}
	if d.boardRowTap("other", 0, 0) || len(tapped) != 3 {
		t.Fatalf("tapped %v", tapped)
	}
}
