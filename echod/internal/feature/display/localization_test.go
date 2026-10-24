//go:build !dot && !spot

package display

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func TestSettingsLanguageInvalidatesBothCaches(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	r := testRenderer()
	var previousBase, previousShell []byte
	for _, lang := range []string{"en", "ja", "en"} {
		if err := config.Set().Screen().Language(lang); err != nil {
			t.Fatal(err)
		}
		r.settingsBase(catGeneral, "General", "Settings")
		if r.baseKey.lang != lang || r.shellKey.lang != lang {
			t.Fatalf("language %s was cached incorrectly", lang)
		}
		if previousBase != nil && (bytes.Equal(previousBase, r.base.Pix) || bytes.Equal(previousShell, r.shell.Pix)) {
			t.Fatal("a language change reused the previous settings image")
		}
		previousBase = bytes.Clone(r.base.Pix)
		previousShell = bytes.Clone(r.shell.Pix)
	}
}

func TestCalendarSelectedSegmentIsIndependentOfLanguage(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	for _, lang := range []string{"en", "ja", "de"} {
		if err := config.Set().Screen().Language(lang); err != nil {
			t.Fatal(err)
		}
		for _, selected := range []calHitKind{calViewDay, calViewWeek, calMonthView} {
			r := testRenderer()
			r.calSegmented(selected, r.w-r.margin, true)
			if len(r.calHits) != 2 {
				t.Fatalf("%s: got %d active segments", lang, len(r.calHits))
			}
			for _, hit := range r.calHits {
				if hit.kind == selected {
					t.Fatalf("%s: selected segment is clickable", lang)
				}
			}
		}
	}
}
