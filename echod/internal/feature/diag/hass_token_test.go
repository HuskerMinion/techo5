package diag

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// Home Assistant's token is taken out of a line that carries it bare, with no "token" in front: the
// panel's whole log goes into the bundle.
func TestTheHomeAssistantTokenIsTakenOut(t *testing.T) {
	was := hass.Path
	hass.Path = filepath.Join(t.TempDir(), "hass.json")
	t.Cleanup(func() { hass.Path = was })
	const token = "eyJhbGciOiJIUzI1NiJ9.a-bare-long-lived-token.c2lnbmVk"
	if err := hass.Get().Set("http://ha.example:8123", token); err != nil {
		t.Fatal(err)
	}
	if got := Redactor().Text("ws auth failed with " + token); strings.Contains(got, token) {
		t.Errorf("the token is still there: %q", got)
	}
}
