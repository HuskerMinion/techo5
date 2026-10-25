//go:build panel

package setup

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A panel's setup page says SSH and Camera on the network are not on this device, rather than "off" and to
// change them somewhere they cannot be changed (techo5-panel#1).
func TestAPanelSaysSSHIsNotHere(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	rec := httptest.NewRecorder()
	privacySection(rec)
	got := rec.Body.String()
	for _, want := range []string{"SSH: not available on this device", "Camera on the network: not available on this device", notHereWhy} {
		if !strings.Contains(got, want) {
			t.Errorf("page has no %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "SSH: off") {
		t.Errorf("page says SSH is off:\n%s", got)
	}
}
