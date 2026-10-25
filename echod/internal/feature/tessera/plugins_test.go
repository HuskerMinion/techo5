//go:build !dot && !spot && !panel

package tessera

import (
	"encoding/json"
	"strings"
	"testing"
)

// A screen's own capabilities name the plugins it draws, as a board's hello does (docs/PLUGINS.md):
// the feature, the plugin API and each plugin with its version and tiles.
func TestHelloNamesThePluginsDrawnHere(t *testing.T) {
	r := NewReceiver(capabilities(grid{1280, 800, 4, 3}), nil)
	r.newID = func() string { return session }
	b, err := json.Marshal(hello(t, r))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"features":["plugins"]`, `"plugin_api":"0.13"`,
		`"plugins":[{"id":"world_clock","version":"0.4.0","tiles":["clocks"]},{"id":"custom_card","version":"0.7.0","tiles":["card"]}]`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("answer %s lacks %s", b, want)
		}
	}
}
