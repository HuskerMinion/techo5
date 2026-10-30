//go:build !dot

package display

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The time zone picker's first two choices, Follow Home Assistant and the common zones, are in the
// screen's language; the regions after them are the zone database's own names (#354).
func TestTheTimeZonePickerSpeaksTheScreenLanguage(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	for lang, want := range map[string][2]string{
		"en": {"Follow Home Assistant", "Common"},
		"it": {"Segui Home Assistant", "Comuni"},
		"de": {"Home Assistant folgen", "Häufige"},
	} {
		if err := config.Set().Screen().Language(lang); err != nil {
			t.Fatal(err)
		}
		p, ok := pickerFor("timezone", sheetView{})
		if !ok || len(p.opts) < 2 || p.opts[0] != want[0] || p.opts[1] != want[1] {
			t.Errorf("%s: the first choices are %v, want %v", lang, p.opts[:min(2, len(p.opts))], want)
		}
		if z := zonePicker(common); z.title != want[1] {
			t.Errorf("%s: the common list is titled %q, want %q", lang, z.title, want[1])
		}
	}
}
