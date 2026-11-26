//go:build dot || spot

package remote

import "net/http"

// A Dot and a Spot have no deck (feature/deck is the Show's), so its routes are not here.

func (f *Remote) deckGet(w http.ResponseWriter) {
	http.Error(w, "this device has no deck", http.StatusNotFound)
}

func (f *Remote) deckPage(w http.ResponseWriter, _, _ string, _ []byte) {
	http.Error(w, "this device has no deck", http.StatusNotFound)
}
