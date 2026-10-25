//go:build panel

package tessera

import (
	"os"
	"path/filepath"
	"testing"
)

// The size a panel's screen says is kept, and is the grid at the next start before the screen opens.
func TestPanelSizeIsKept(t *testing.T) {
	sizePath = filepath.Join(t.TempDir(), "tessera-screen")
	sizeKnown = grid{}
	if got := screenGrid(); got != gridFor(true, false) {
		t.Fatalf("before any size: %v, want a Show 8's", got)
	}
	SetPanelSize(2560, 1600)
	if b, _ := os.ReadFile(sizePath); string(b) != "2560x1600" {
		t.Fatalf("kept %q", b)
	}
	sizeKnown = grid{} // the next start
	if got, want := screenGrid(), (grid{2560, 1600, 4, 3}); got != want {
		t.Fatalf("after a restart: %v, want %v", got, want)
	}
}
