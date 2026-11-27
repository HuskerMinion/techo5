package detect

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// A model the daemon would refuse is refused by the tools too, with the reason, rather than run and
// scoring nothing (#768).
func TestNewDetectorRefusesWhatTheDaemonWould(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.tflite")
	if err := os.WriteFile(path, []byte("not a model at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The engine itself loads a good model; only the daemon's check sees the file named as the model's.
	good := filepath.Join("..", "..", "..", "third_party", "microwakeword", "testdata", "okay_nabu.tflite")
	m := wake.Model{Path: path}
	m.Config.ModelPath, m.Config.ProbabilityCutoff, m.Config.SlidingWindowSize = good, 0.9, 5
	if _, err := NewDetector(m); err == nil {
		t.Fatal("a model the daemon's check refuses was loaded for a tool")
	}
	m.Path = good
	if _, err := NewDetector(m); err != nil {
		t.Fatalf("a good model: %v", err)
	}
}
