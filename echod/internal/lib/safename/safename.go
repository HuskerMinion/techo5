// Package safename keeps an identifier that arrived from somewhere else from naming a file
// somewhere else.
//
// Three kinds of identifier reach a path in this daemon, and the device chose none of them:
//
//   - a recording id, which arrives in the call Home Assistant makes to play back a turn's audio
//     (feature/recording);
//   - a wake word id, which arrives in the list of models Home Assistant offers and comes back in
//     the selection that downloads one (lib/wake);
//   - the model file name a wake word's manifest carries, which is JSON fetched over the network or
//     copied onto the device by hand (lib/wake).
//
// Each is joined to a directory and then read, written or deleted, as root. An id holding "..", a
// separator or a drive letter would name a file outside that directory, so an id that is not a plain
// file name is refused here rather than repaired: an id quietly rewritten no longer stands for what
// the other side asked about, and the caller goes on believing it does.
package safename

import (
	"errors"
	"fmt"
	"path/filepath"
)

// ErrName is what a name that cannot stand for a file fails with, for a caller that wants to tell
// this apart from the read or write that follows.
var ErrName = errors.New("not a plain file name")

// OK reports whether name may be joined to a directory and used as a file in it: a single element,
// not one of the two that walk the tree, and nothing a path is built out of.
//
// The backslash and the colon are refused as well as the slash. This runs on the device, where
// neither is a separator, but the tests and the tools run on Windows, where both are, and a rule
// that holds only on one of them is a rule nobody can reason about.
func OK(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		switch {
		case r == '/' || r == '\\' || r == ':':
			return false
		case r < 0x20 || r == 0x7f:
			return false
		}
	}
	return true
}

// Join is filepath.Join for a name that came from outside: the path when the name is one this
// directory can hold, and an error naming it when it is not.
func Join(dir, name string) (string, error) {
	if !OK(name) {
		return "", fmt.Errorf("%q: %w", name, ErrName)
	}
	return filepath.Join(dir, name), nil
}
