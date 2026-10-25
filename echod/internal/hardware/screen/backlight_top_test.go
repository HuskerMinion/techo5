//go:build !dot && !panel

package screen

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTheBacklightTopIsReadFromTheDriver(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		name, content string
		want          int
	}{
		{"the Skylight's interpolated table", "235\n", 235},
		{"a plain number", "1023", 1023},
		{"zero is not a top", "0\n", 99},
		{"a negative number", "-5", 99},
		{"garbage", "max\n", 99},
	} {
		p := filepath.Join(dir, "max_brightness")
		if err := os.WriteFile(p, []byte(c.content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := readTop(p, 99); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
	if got := readTop(filepath.Join(dir, "missing"), 99); got != 99 {
		t.Errorf("a missing node: %d, want the fallback", got)
	}
}
