package media

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/sendspin/secure"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// The device is looked for in Music Assistant by its Sendspin key first, its player's id since
// Sendspin is encrypted, so a device whose MAC player went unavailable, or that never had one, is
// still found (#738).
func TestLibraryPlayerIDsHaveTheKeyFirst(t *testing.T) {
	was := layout.SendspinKeysPath
	layout.SendspinKeysPath = filepath.Join(t.TempDir(), "sendspin-keys.json")
	t.Cleanup(func() { layout.SendspinKeysPath = was })

	// No keys yet: whatever the device has (its MAC, where it can read it), and no key made for it.
	ids, _ := LibraryPlayerIDs()
	if len(ids) > 1 {
		t.Fatalf("with no keys: %v", ids)
	}
	if _, err := secure.ClientIDAt(layout.SendspinKeysPath); err == nil {
		t.Fatal("looking made a key")
	}

	st, err := secure.Open(layout.SendspinKeysPath)
	if err != nil {
		t.Fatal(err)
	}
	ids, err = LibraryPlayerIDs()
	if err != nil || len(ids) == 0 || ids[0] != st.ClientID() {
		t.Fatalf("got %v, %v; want the key %q first", ids, err, st.ClientID())
	}
}
