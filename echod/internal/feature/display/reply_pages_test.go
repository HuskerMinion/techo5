//go:build !dot

package display

import (
	"slices"
	"testing"
)

// A reply too long for the screen is shown a page at a time, the page the voice is on.
func TestALongReplyTurnsItsPagesWithTheVoice(t *testing.T) {
	lines := []string{"aaaa", "bbbb", "cccc", "dddd", "eeee"} // 20 characters, pages of 2 lines
	for _, c := range []struct {
		said float64
		want []string
	}{
		{0, []string{"aaaa", "bbbb …"}},
		{0.35, []string{"aaaa", "bbbb …"}},
		{0.45, []string{"cccc", "dddd …"}},
		{0.79, []string{"cccc", "dddd …"}},
		{0.85, []string{"eeee"}},
		{1, []string{"eeee"}},
	} {
		if got := replyPage(lines, 2, c.said); !slices.Equal(got, c.want) {
			t.Errorf("said %.2f: page %q, want %q", c.said, got, c.want)
		}
	}
}

// A reply that fits is shown whole, whatever has been said.
func TestAReplyThatFitsIsOnePage(t *testing.T) {
	lines := []string{"one", "two"}
	if got := replyPage(lines, 3, 0.9); !slices.Equal(got, lines) {
		t.Errorf("page %q, want all of it", got)
	}
}
