package setup

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/security"
)

// The section gives /screen.png's address with its key only while the page is on, on a device that has
// it, and a new key replaces the old one.
func TestScreenSectionGivesTheAddressWithItsKey(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	rec := httptest.NewRecorder()
	screenPageSection(rec, "tok")
	got := rec.Body.String()
	if !security.ScreenPage() {
		if got != "" {
			t.Errorf("a device with no screen shows a screen section:\n%s", got)
		}
		return
	}
	if strings.Contains(got, "/screen.png?key=") || !strings.Contains(got, "is off") {
		t.Errorf("off, the section shows:\n%s", got)
	}
	if msg := saveScreenKey(nil); msg == "" {
		t.Error("a key was replaced with the page off")
	}

	_ = config.Set().Security().ScreenKey("abc123")
	_ = config.Set().Security().Screen(true)
	rec = httptest.NewRecorder()
	screenPageSection(rec, "tok")
	got = rec.Body.String()
	if !strings.Contains(got, `href="/screen.png?key=abc123"`) || !strings.Contains(got, "Generic Camera") ||
		!strings.Contains(got, `value="screenkey"`) {
		t.Errorf("on, the section shows:\n%s", got)
	}
	if msg := saveScreenKey(nil); msg != "" || config.Get().Security.ScreenKey == "abc123" {
		t.Errorf("not replaced: %q", msg)
	}
}
