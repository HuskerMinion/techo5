package setup

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The section shows the link only while the monitor is on, and says plainly who can hear.
func TestBabySectionShowsTheLinkOnlyWhileOn(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	rec := httptest.NewRecorder()
	babySection(rec, "tok")
	if got := rec.Body.String(); strings.Contains(got, "/baby?k=") || !strings.Contains(got, "is off") {
		t.Errorf("off, the section shows:\n%s", got)
	}

	if err := config.Set().Security().BabyKey("abc123"); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Security().Baby(true); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	babySection(rec, "tok")
	got := rec.Body.String()
	if !strings.Contains(got, `href="/baby?k=abc123"`) || !strings.Contains(got, "anyone on your home network who has the link") ||
		!strings.Contains(got, "mute button stops the sound") {
		t.Errorf("on, the section shows:\n%s", got)
	}
}

// Replacing the link is refused while the monitor is off, and changes it while on.
func TestNewBabyLink(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if msg := saveBabyLink(nil); msg == "" {
		t.Error("a link was replaced with the monitor off")
	}
	_ = config.Set().Security().Baby(true)
	_ = config.Set().Security().BabyKey("old")
	if msg := saveBabyLink(nil); msg != "" || config.Get().Security.BabyKey == "old" {
		t.Errorf("not replaced: %q", msg)
	}
}
