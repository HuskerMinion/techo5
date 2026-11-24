//go:build panel

package setup

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// A Panel's setup page says in a line that add-ons are not part of it, and offers nothing to install.
func TestThePanelSaysAddonsAreNotHere(t *testing.T) {
	w := httptest.NewRecorder()
	addonsSection(w, "tok")
	body := w.Body.String()
	if !strings.Contains(body, "Add-ons are not part of TECHO5 Panel") || strings.Contains(body, "<form") {
		t.Errorf("the Panel's add-ons card:\n%s", body)
	}
}
