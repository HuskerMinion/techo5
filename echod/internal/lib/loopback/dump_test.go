//go:build panel_debug

package loopback

import (
	"os"
	"path/filepath"
	"testing"
)

// A debug build records while the flag file is there.
func TestTheDebugRecorderFollowsTheFlag(t *testing.T) {
	flag := filepath.Join(t.TempDir(), "dump-echo")
	d := Dump{FlagPath: flag}
	if d.On() {
		t.Fatal("on with no flag")
	}
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	d = Dump{FlagPath: flag}
	if !d.On() {
		t.Fatal("off with the flag there")
	}
}
