//go:build !panel

package setup

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Off a panel SSH and the camera page are shown on or off, with nothing about them missing.
func TestSSHIsShownOnOrOff(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	rec := httptest.NewRecorder()
	privacySection(rec)
	got := rec.Body.String()
	if !strings.Contains(got, "SSH: off · Camera on the network: off · Screen on the network: off") {
		t.Errorf("page does not show the switches:\n%s", got)
	}
	if strings.Contains(got, "not available") || !strings.Contains(got, "or in Home\n\t  Assistant.</p>") {
		t.Errorf("page changed:\n%s", got)
	}
}
