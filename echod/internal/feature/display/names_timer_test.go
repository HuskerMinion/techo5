//go:build !dot

package display

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A timer named in the screen's language is not called a timer a second time: "Cronometru paste" is
// done as it is, where "Paste" is the paste timer. English names keep working under every language.
func TestNamesTimerInTheScreenLanguage(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	for _, c := range []struct {
		lang, name string
		want       bool
	}{
		{"en", "Pasta timer", true},
		{"en", "Pasta", false},
		{"ro", "Cronometru paste", true},
		{"ro", "cronometrul pentru ouă", true},
		{"ro", "Paste", false},
		{"ro", "Pasta timer", true},
		{"de", "Nudeln", false},
	} {
		_ = config.Set().Screen().Language(c.lang)
		if got := namesTimer(c.name); got != c.want {
			t.Errorf("%s: namesTimer(%q) = %v, want %v", c.lang, c.name, got, c.want)
		}
	}
}
