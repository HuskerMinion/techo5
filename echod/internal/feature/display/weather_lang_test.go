//go:build !dot && !spot

package display

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The weather page's high, low and rain come from the catalog, as the Spot's already did.
func TestWeatherLabelsInTheScreensLanguage(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	cases := []struct {
		lang, rangeLine, rainLine string
	}{
		{"en", "High 10°   Low 6°", "Rain 23%"},
		{"de", "Max 10°   Min 6°", "Regen 23%"},
		{"fr", "Max 10°   Min 6°", "Pluie 23%"},
		{"es", "Máx 10°   Mín 6°", "Lluvia 23%"},
	}
	for _, c := range cases {
		if err := config.Set().Screen().Language(c.lang); err != nil {
			t.Fatal(err)
		}
		if got := weatherRange(10, 6); got != c.rangeLine {
			t.Errorf("%s: range %q, want %q", c.lang, got, c.rangeLine)
		}
		if got := weatherRain(23); got != c.rainLine {
			t.Errorf("%s: rain %q, want %q", c.lang, got, c.rainLine)
		}
	}
}
