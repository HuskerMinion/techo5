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
