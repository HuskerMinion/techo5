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
	keepOverOwn()
	kept, _ := os.Stat(Conf)
	if !kept.ModTime().After(installed) {
		t.Fatalf("kept %v is not after wifi.conf %v", kept.ModTime(), installed)
	}

	// Already newer: left as it is.
	later := installed.Add(time.Hour)
	if err := os.Chtimes(Conf, later, later); err != nil {
		t.Fatal(err)
	}
	keepOverOwn()
	kept, _ = os.Stat(Conf)
	if !kept.ModTime().Equal(later) {
		t.Fatalf("a newer file was re-dated to %v", kept.ModTime())
	}
}
