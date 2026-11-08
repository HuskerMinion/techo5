//go:build dot && linux && !panel

package improv

import "testing"

func TestEveryPromptIsThere(t *testing.T) {
	for _, line := range []string{"offer", "offer_press", "joined_setup", "failed"} {
		n := said(line)
		if len(n) != 1 || n[0].Ms < 2000 || n[0].Ms > 12000 {
			t.Errorf("%s: %v, want one note of a few seconds", line, n)
		}
	}
	if said("no such line") != nil {
		t.Error("a line that is not there was said")
	}
}
