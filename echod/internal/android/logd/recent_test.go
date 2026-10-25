package logd

import (
	"fmt"
	"strings"
	"testing"
)

// What is kept of the log is its last lines, oldest first, and bounded both by lines and by bytes:
// a line far too long is cut rather than pushing everything else out.
func TestRecentIsBounded(t *testing.T) {
	var r recentLog
	for i := range recentLines + 10 {
		r.add(fmt.Sprintf("I [%d.00] line %d", i, i))
	}
	if len(r.lines) != recentLines {
		t.Fatalf("%d lines kept, want %d", len(r.lines), recentLines)
	}
	if want := fmt.Sprintf("line %d", recentLines+9); !strings.HasSuffix(r.lines[len(r.lines)-1], want) {
		t.Errorf("last kept %q, want it to end %q", r.lines[len(r.lines)-1], want)
	}
	if !strings.HasSuffix(r.lines[0], "line 10") {
		t.Errorf("first kept %q, want line 10", r.lines[0])
	}

	r.add(strings.Repeat("x", 4*recentLineMax))
	if got := r.lines[len(r.lines)-1]; len(got) > recentLineMax+8 {
		t.Errorf("a long line is kept %d bytes long", len(got))
	}
	for range 2 * recentBytes / recentLineMax {
		r.add(strings.Repeat("y", recentLineMax))
	}
	if r.bytes > recentBytes || len(r.lines) == 0 {
		t.Errorf("%d bytes in %d lines kept, want at most %d", r.bytes, len(r.lines), recentBytes)
	}
	n := 0
	for _, l := range r.lines {
		n += len(l)
	}
	if n != r.bytes {
		t.Errorf("counted %d bytes, have %d", r.bytes, n)
	}
}
