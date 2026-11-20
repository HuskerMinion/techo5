//go:build !dot

package setup

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/tailscale"
)

// Where the built-in NetBird's card was, the card points to the add-on that took its place (#580), and
// has nothing to join with.
func TestTheNetbirdCardPointsToTheAddOn(t *testing.T) {
	was := tailscale.Available
	t.Cleanup(func() { tailscale.Available = was })
	tailscale.Available = func() bool { return true }
	w := httptest.NewRecorder()
	netbirdSection(w, "token")
	body := w.Body.String()
	for _, want := range []string{`id="sec-netbird"`, `href="https://github.com/tioan/techo5-netbird"`,
		`href="https://github.com/HuskerMinion/techo5/blob/main/docs/external-asp.md#add-ons-others-have-made"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the card lacks %s:\n%s", want, body)
		}
	}
	if strings.Contains(body, "<form") {
		t.Error("the card still has a form")
	}
	tailscale.Available = func() bool { return false }
	w = httptest.NewRecorder()
	netbirdSection(w, "token")
	if w.Body.Len() != 0 {
		t.Error("a device the built-in never ran on shows the card")
	}
}
