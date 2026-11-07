//go:build dot

package wifi

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A join on a Dot whose clock is years behind (it booted with no network, so no NTP) still leaves the
// supplicant's file dated after the installer's wifi.conf, or techo5-net rebuilds it from that at the
// next boot and the network joined is gone.
func TestAJoinOutlastsTheBootOnAClockBehind(t *testing.T) {
	dir := t.TempDir()
	wasConf, wasOwn := Conf, ownConf
	t.Cleanup(func() { Conf, ownConf = wasConf, wasOwn })
	Conf, ownConf = filepath.Join(dir, "wpa_supplicant.conf"), filepath.Join(dir, "wifi.conf")

	for _, f := range []string{Conf, ownConf} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	installed := time.Now().Add(24 * time.Hour) // the installer's clock, ahead of this one
	if err := os.Chtimes(ownConf, installed, installed); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(Conf, "network={}\n"); err != nil {
		t.Fatal(err)
	}
	kept, _ := os.Stat(Conf)
	// busybox's test -nt: whole seconds, strictly later.
	if kept.ModTime().Unix() <= installed.Unix() {
		t.Fatalf("kept %v is not a second after wifi.conf %v", kept.ModTime(), installed)
	}

	// With the clock past wifi.conf, it is simply now.
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(ownConf, past, past); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(Conf, "network={}\n"); err != nil {
		t.Fatal(err)
	}
	kept, _ = os.Stat(Conf)
	if d := time.Since(kept.ModTime()); d < 0 || d > time.Minute {
		t.Fatalf("dated %v, not now", kept.ModTime())
	}

	// Only the supplicant's file: .prev is the Dot's clock's.
	if err := os.Chtimes(ownConf, installed, installed); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(Conf+".prev", "x"); err != nil {
		t.Fatal(err)
	}
	prev, _ := os.Stat(Conf + ".prev")
	if prev.ModTime().Unix() > installed.Unix() {
		t.Fatal(".prev was re-dated")
	}
}
