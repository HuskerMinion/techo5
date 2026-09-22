package api

import (
	"os"
	"path/filepath"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"
)

// The key echoctl writes, in the form Home Assistant asks for.
const testKey = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="

// A device that has never been paired has no key file, and that is the one case where coming up on
// the zero key is right: Home Assistant can then push a real one.
func TestNoKeyFileMeansUnprovisioned(t *testing.T) {
	psk, err := loadPSK(filepath.Join(t.TempDir(), "psk"))
	if err != nil {
		t.Fatalf("a device with no key should still start: %v", err)
	}
	if !psk.IsZero() {
		t.Errorf("a device with no key got a key from somewhere")
	}
}

// A key that is there and cannot be read is the case that matters: the device is paired, and starting
// anyway would let anyone on the network in. A directory where the file should be is the portable way
// to get an error that is not "no such file".
func TestAnUnreadableKeyIsNotAMissingKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "psk")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("setting up the test: %v", err)
	}

	psk, err := loadPSK(path)
	if err == nil {
		t.Fatalf("an unreadable key started the server anyway, with zero=%v", psk.IsZero())
	}
	if psk != nil {
		t.Errorf("a key came back alongside the error: %v", psk)
	}
}

// An empty file is a key that was written badly, not a key that was never written, so it fails the
// same way rather than quietly dropping authentication.
func TestAnEmptyKeyFileFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "psk")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("setting up the test: %v", err)
	}

	if _, err := loadPSK(path); err == nil {
		t.Errorf("an empty key file was taken for a key")
	}
}

// And the ordinary case: the key on disk is the key the server serves.
func TestAKeyOnDiskIsUsed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "psk")
	if err := os.WriteFile(path, []byte(testKey+"\n"), 0o600); err != nil {
		t.Fatalf("setting up the test: %v", err)
	}

	psk, err := loadPSK(path)
	if err != nil {
		t.Fatalf("a good key was refused: %v", err)
	}
	want, err := esphome.ParsePSK(testKey)
	if err != nil {
		t.Fatalf("the test's own key does not parse: %v", err)
	}
	if *psk != want {
		t.Errorf("the key served is not the key on disk")
	}
}
