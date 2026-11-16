package announce

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// An older device that sends the house word itself is heard for what older devices sent, but a pop-up
// dismissal is new and only ever signed: the plain word carries no nonce, so it is not taken for it.
func TestAPopupDismissalIsOnlyTakenSigned(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Home().HouseWord("bluebird"); err != nil {
		t.Fatal(err)
	}
	f := &Feature{}
	body, headers := encode(Message{From: "Kitchen", Kind: KindPopupDismissed, ID: "popup-0123456789abcdef01234567"})
	r := httptest.NewRequest(http.MethodPost, "/announce", bytes.NewReader(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	r.Header.Set(header, "bluebird")
	w := httptest.NewRecorder()
	f.receive(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("a dismissal with the plain word answered %d, want 403", w.Code)
	}
}

// A dismissal names its event the way a Show does, or it is turned away before it can be kept.
func TestAPopupIDHasTheShowsShape(t *testing.T) {
	for id, want := range map[string]bool{
		"popup-0123456789abcdef01234567":  true,
		"popup-0123456789ABCDEF01234567":  false,
		"popup-0123456789abcdef0123456":   false,
		"popup-0123456789abcdef012345678": false,
		"popup-0123456789abcdef0123456g":  false,
		"other-0123456789abcdef01234567":  false,
		"":                                false,
	} {
		if got := popupID(id); got != want {
			t.Errorf("popupID(%q) = %v, want %v", id, got, want)
		}
	}
}
