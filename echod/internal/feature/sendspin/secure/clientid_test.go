package secure

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// The client id read from the file is the one the open store gives, and reading it makes nothing: a
// device with no keys yet is not given an identity by a look at its Music Assistant player (#738).
func TestClientIDAtReadsWithoutMaking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	if _, err := ClientIDAt(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("no file: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("reading the id made a store")
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ClientIDAt(path)
	if err != nil || got != st.ClientID() {
		t.Fatalf("got %q, %v; the store says %q", got, err, st.ClientID())
	}
}
