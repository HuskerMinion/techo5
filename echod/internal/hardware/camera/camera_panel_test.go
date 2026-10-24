//go:build panel

package camera

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

func TestPanelCameraModeMarker(t *testing.T) {
	previous := layout.StateDir
	layout.StateDir = t.TempDir()
	t.Cleanup(func() { layout.StateDir = previous })
	marker := filepath.Join(layout.StateDir, "camera-ready")
	for _, test := range []struct {
		value  string
		width  int
		height int
	}{
		{"1280 720\n", 1280, 720},
		{"640 480\n", 640, 480},
		{"2592 1944\n", 640, 480}, // RTP/JPEG cannot carry this as live video.
		{"garbage", 640, 480},
	} {
		if err := os.WriteFile(marker, []byte(test.value), 0600); err != nil {
			t.Fatal(err)
		}
		width, height := panelOutputSize()
		if width != test.width || height != test.height {
			t.Fatalf("marker %q: got %dx%d, want %dx%d", test.value, width, height, test.width, test.height)
		}
	}
}
