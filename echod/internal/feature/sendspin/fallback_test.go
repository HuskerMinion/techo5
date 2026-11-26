package sendspin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/sendspin/secure"
)

var errServerHungUp = fmt.Errorf("%w: %v", secure.ErrHungUp, io.EOF)

func openStore(t *testing.T, path string) *secure.Store {
	t.Helper()
	st, err := secure.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// A server the device has never spoken encrypted with is still taken for one from before encryption
// on a hang-up, or on a reply that is not server/init, as before.
func TestAServerNeverSeenEncryptedStillFallsBack(t *testing.T) {
	st := openStore(t, filepath.Join(t.TempDir(), "keys.json"))
	if !fallBack(st, "192.0.2.1", errServerHungUp) {
		t.Error("a hang-up from a server never seen encrypted does not fall back")
	}
	if !fallBack(st, "192.0.2.1", secure.ErrNotEncrypted) {
		t.Error("an old server's reply does not fall back")
	}
	if fallBack(st, "192.0.2.1", nil) || fallBack(st, "192.0.2.1", errors.New("connection reset")) {
		t.Error("a handshake that did not end as an old server's falls back")
	}
}

// A server that has spoken encrypted is not taken for one from before encryption on a hang-up or
// silence, even after a restart, so the device does not swap to the player under its MAC (#721).
func TestAServerSeenEncryptedIsNotMarkedUnencryptedOnAHangUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := openStore(t, path).SpokeEncrypted("192.0.2.1", "server-a"); err != nil {
		t.Fatal(err)
	}
	st := openStore(t, path) // a restart
	for range 3 {
		if fallBack(st, "192.0.2.1", errServerHungUp) {
			t.Fatal("a hang-up from a server that spoke encrypted falls back")
		}
	}
	if !fallBack(st, "192.0.2.2", errServerHungUp) {
		t.Error("a hang-up from another server does not fall back")
	}
	// A reply that is not server/init says outright that the server is old, as a downgrade would.
	if !fallBack(st, "192.0.2.1", secure.ErrNotEncrypted) {
		t.Error("an old server's reply from a server that spoke encrypted does not fall back")
	}
}

// A server that has hung up on every encrypted handshake for downgradeAfter, with none completed in
// between, is a downgraded one and falls back.
func TestAServerHangingUpForADayFallsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	st := openStore(t, path)
	if err := st.SpokeEncrypted("192.0.2.1", "server-a"); err != nil {
		t.Fatal(err)
	}
	if fallBack(st, "192.0.2.1", errServerHungUp) {
		t.Fatal("the first hang-up falls back")
	}
	backdate(t, path, downgradeAfter+time.Minute)
	st = openStore(t, path)
	if !fallBack(st, "192.0.2.1", errServerHungUp) {
		t.Error("a server hanging up for longer than downgradeAfter does not fall back")
	}

	// An encrypted handshake in the meantime ends the streak.
	if err := st.SpokeEncrypted("192.0.2.1", "server-a"); err != nil {
		t.Fatal(err)
	}
	if fallBack(st, "192.0.2.1", errServerHungUp) {
		t.Error("a hang-up after an encrypted handshake falls back")
	}
}

// backdate moves the start of every failing streak in the keys file at path back by d.
func backdate(t *testing.T, path string, d time.Duration) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f map[string]any
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	moved := 0
	for _, e := range f["encrypted"].([]any) {
		e := e.(map[string]any)
		if s, ok := e["failing"].(string); ok {
			at, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				t.Fatal(err)
			}
			e["failing"] = at.Add(-d).Format(time.RFC3339Nano)
			moved++
		}
	}
	if moved == 0 {
		t.Fatal("no failing streak to move")
	}
	if b, err = json.Marshal(f); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
