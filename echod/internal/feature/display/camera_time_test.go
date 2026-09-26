package display

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Each Camera time choice keeps a camera opened from the screen up for as long as it says; nothing
// chosen is the minute it has always been, and a value no choice has is that minute too.
func TestCameraScreenTime(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if got := cameraScreenTime(); got != time.Minute {
		t.Fatalf("default %v", got)
	}
	s := cameraTimeSelect()
	for label, want := range map[string]time.Duration{
		"5 minutes":    5 * time.Minute,
		"1 hour":       time.Hour,
		"Until tapped": untilTapped,
		"1 minute":     time.Minute,
	} {
		s.OnCommand(label)
		if got := cameraScreenTime(); got != want {
			t.Errorf("%s: %v, want %v", label, got, want)
		}
	}
	if err := config.Set().Screen().CameraMinutes(7); err != nil {
		t.Fatal(err)
	}
	if got := cameraScreenTime(); got != time.Minute {
		t.Errorf("an unknown value: %v", got)
	}
}
