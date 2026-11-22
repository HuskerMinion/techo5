package secure

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Discarding removes the identity for good: a pairing still being made writes nothing back, and the
// next Open is a new device.
func TestADiscardedStoreIsGoneForGood(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	old := st.ClientID()
	if err := st.Discard(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the file is still there (%v)", err)
	}
	if err := st.remember(strings.Repeat("a", 43), bytes.Repeat([]byte{1}, 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a pairing after the discard wrote the old identity back")
	}
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.ClientID() == old {
		t.Error("the identity after the discard is the old one")
	}
}
