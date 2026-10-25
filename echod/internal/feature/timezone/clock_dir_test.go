//go:build !panel

package timezone

import (
	"os"
	"path/filepath"
	"testing"
)

// NTP has the clock once boot's mark is there, and not before.
func TestNTPSynced(t *testing.T) {
	was := ntpSet
	t.Cleanup(func() { ntpSet = was })
	ntpSet = filepath.Join(t.TempDir(), "ntp-set")
	if ntpSynced() {
		t.Error("synced with no mark")
	}
	if err := os.WriteFile(ntpSet, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !ntpSynced() {
		t.Error("not synced with the mark there")
	}
}
