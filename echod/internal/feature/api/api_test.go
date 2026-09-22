package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// shorten makes the wait for a key something a test can sit through.
func shorten(t *testing.T, retry, wait time.Duration) {
	t.Helper()
	wasRetry, wasWait := keyRetry, keyWait
	keyRetry, keyWait = retry, wait
	t.Cleanup(func() { keyRetry, keyWait = wasRetry, wasWait })
}

// A read that fails and then works is the case this is all for: the flash was busy, or the
// filesystem was still coming up. Giving up on the first error left the device off Home Assistant
// until somebody pulled the power, because this service is started once and a start that fails is
// never tried again.
func TestAKeyThatReadsLaterIsWaitedFor(t *testing.T) {
	shorten(t, 10*time.Second, 5*time.Millisecond)
	path := filepath.Join(t.TempDir(), "psk")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("setting up the test: %v", err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		if err := os.Remove(path); err != nil {
			return
		}
		_ = os.WriteFile(path, []byte(testKey+"\n"), 0o600)
	}()

	psk, err := waitForPSK(context.Background(), path)
	if err != nil {
		t.Fatalf("a key that came back was not waited for: %v", err)
	}
	want, err := esphome.ParsePSK(testKey)
	if err != nil {
		t.Fatalf("the test's own key does not parse: %v", err)
	}
	if *psk != want {
		t.Errorf("the key served is not the key that turned up")
	}
}

// And a key that never reads: waiting has to end, and it has to end with the error rather than with
// a device serving the reserved zero key to anyone on the network.
func TestAKeyThatNeverReadsFailsRatherThanServingOnZero(t *testing.T) {
	shorten(t, 50*time.Millisecond, 5*time.Millisecond)
	path := filepath.Join(t.TempDir(), "psk")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("setting up the test: %v", err)
	}

	psk, err := waitForPSK(context.Background(), path)
	if err == nil {
		t.Fatalf("an unreadable key started the server anyway, with zero=%v", psk.IsZero())
	}
	if psk != nil {
		t.Errorf("a key came back alongside the error: %v", psk)
	}
}

// A device being shut down while it waits stops waiting: the whole point is to hold up nothing but
// the API's own start.
func TestWaitingForAKeyEndsWhenTheDeviceIsStopping(t *testing.T) {
	shorten(t, time.Hour, 5*time.Millisecond)
	path := filepath.Join(t.TempDir(), "psk")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("setting up the test: %v", err)
	}
	ctx, stop := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		stop()
	}()

	done := make(chan error, 1)
	go func() {
		_, err := waitForPSK(ctx, path)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a cancelled wait came back with a key")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the wait for a key ignored the device being stopped")
	}
}

// A device that has never been paired is not waited for at all: no key file is an answer, not a
// failure, and holding the boot up for it would be waiting for something that is not coming.
func TestAMissingKeyFileIsNotWaitedFor(t *testing.T) {
	shorten(t, time.Hour, time.Hour)
	began := time.Now()
	psk, err := waitForPSK(context.Background(), filepath.Join(t.TempDir(), "psk"))
	if err != nil {
		t.Fatalf("a device with no key should still start: %v", err)
	}
	if !psk.IsZero() {
		t.Errorf("a device with no key got a key from somewhere")
	}
	if took := time.Since(began); took > 5*time.Second {
		t.Errorf("an unpaired device waited %v for a key that was never coming", took)
	}
}
