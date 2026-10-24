//go:build panel

package camera

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
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

func TestPanelCameraOffDropsStillAndRefusesCapture(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	c := &Camera{
		last:    &Frame{rawJPEG: []byte("preview")},
		still:   &Frame{rawJPEG: []byte("private still")},
		stillAt: time.Now(),
	}
	c.Disable()
	if c.last != nil || c.still != nil || !c.stillAt.IsZero() {
		t.Fatal("camera off kept a preview or still")
	}
	if _, err := c.Snapshot(context.Background()); !errors.Is(err, ErrOff) {
		t.Fatalf("snapshot while off: %v", err)
	}
	if _, err := c.Acquire(); !errors.Is(err, ErrOff) {
		t.Fatalf("video while off: %v", err)
	}
}
