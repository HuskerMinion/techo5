//go:build !panel_debug

package loopback

import (
	"os"
	"path/filepath"
	"testing"
)

// A release build never records, even with the flag file there.
func TestNoRecorderWithoutTheDebugTag(t *testing.T) {
	flag := filepath.Join(t.TempDir(), "dump-echo")
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	d := Dump{FlagPath: flag}
	if d.On() {
		t.Fatal("the recorder is on in a build without panel_debug")
	}
}
