package sendspin

import (
	"os"
	"path/filepath"
	"testing"
)

// Discarding (Leave Home Assistant) removes the identity and hands out no token until the restart.
func TestDiscardForgetsTheIdentityAndTheToken(t *testing.T) {
	was := keysPath
	keysPath = filepath.Join(t.TempDir(), "sendspin-keys.json")
	reset := func() {
		keyStore.Lock()
		keyStore.st, keyStore.err, keyStore.discarded = nil, nil, false
		keyStore.Unlock()
	}
	reset()
	t.Cleanup(func() { keysPath = was; reset() })

	if PairingToken() == "" {
		t.Fatal("no token before discarding")
	}
	if err := Discard(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keysPath); !os.IsNotExist(err) {
		t.Errorf("the keys file is still there (%v)", err)
	}
	if id, tok, _ := Pairing(); id != "" || tok != "" || PairingToken() != "" {
		t.Error("a client id or token is still handed out after discarding")
	}
	if _, err := os.Stat(keysPath); !os.IsNotExist(err) {
		t.Error("asking for the token made a new identity before the restart")
	}
}

// A device whose keys were never made discards without making any.
func TestDiscardWithNoKeysMakesNone(t *testing.T) {
	was := keysPath
	keysPath = filepath.Join(t.TempDir(), "sendspin-keys.json")
	t.Cleanup(func() {
		keysPath = was
		keyStore.Lock()
		keyStore.st, keyStore.err, keyStore.discarded = nil, nil, false
		keyStore.Unlock()
	})
	if err := Discard(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keysPath); !os.IsNotExist(err) {
		t.Error("discarding made a keys file")
	}
}
