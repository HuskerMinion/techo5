//go:build !dot

package display

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Every clock style has a name on the screen in the screen's language. In English it is the name Home
// Assistant and the setup page choose it by, which stays English in every language.
func TestClockStyleNamesAreTranslated(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	for i, st := range clockStyles {
		if got := clockStyleName(i); got != st.label {
			t.Errorf("style %q is %q on an English screen, want %q", st.value, got, st.label)
		}
	}
	if err := config.Set().Screen().Language("de"); err != nil {
		t.Fatal(err)
	}
	if got := clockStyleName(0); got != "Klassisch" {
		t.Errorf("the classic style in German is %q", got)
	}
	if got := clockStyleOptions()[0]; got != "Classic" {
		t.Errorf("Home Assistant's name for the classic style is %q on a German screen", got)
	}
	names := clockStyleNames()
	for i, st := range clockStyles {
		if names[i] == "" {
			t.Errorf("style %q has no German name", st.value)
		}
	}
}
