package timezone

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidServer(t *testing.T) {
	for s, want := range map[string]bool{
		"192.168.1.1": true, "ntp.lan": true, "fd00::1": true, "time-1.home.arpa": true,
		"-q": false, "a b": false, "ntp;reboot": false, "$(x)": false, "": true,
	} {
		if got := validServer(s); got != want {
			t.Errorf("validServer(%q) = %v, want %v", s, got, want)
		}
	}
}

// Saved, read back, refused when it is not a server, and cleared with an empty one.
func TestSetServer(t *testing.T) {
	was, restart := serverFile, restartNTP
	t.Cleanup(func() { serverFile, restartNTP = was, restart })
	restarts := 0
	restartNTP = func() { restarts++ }
	serverFile = filepath.Join(t.TempDir(), "ntp-server")
	if err := SetServer(" 192.168.1.1 "); err != nil || Server() != "192.168.1.1" {
		t.Fatalf("saved %q (%v), want 192.168.1.1", Server(), err)
	}
	if err := SetServer("ntp;reboot"); err == nil || Server() != "192.168.1.1" {
		t.Errorf("a bad server was taken (%v), now %q", err, Server())
	}
	if err := SetServer(""); err != nil || Server() != "" {
		t.Errorf("cleared to %q (%v)", Server(), err)
	}
	if _, err := os.Stat(serverFile); !os.IsNotExist(err) {
		t.Errorf("the file is still there: %v", err)
	}
	if restarts != 2 {
		t.Errorf("ntpd started again %d times, want 2 (set, cleared)", restarts)
	}
}
