//go:build panel

package diag

import (
	"context"
	"strings"
	"testing"
)

// A panel's network section says Android runs the Wi-Fi, not that a tablet that is online has no
// connection because there is no wpa_supplicant to ask (techo5-panel#1).
func TestAPanelsNetworkIsAndroids(t *testing.T) {
	got := networkSection(context.Background())
	for _, want := range []string{"wifi: managed by Android", "addresses reported by the app: ", "home assistant access: set="} {
		if !strings.Contains(got, want) {
			t.Errorf("network section %q, want %q in it", got, want)
		}
	}
	if strings.Contains(got, "connected=false") || strings.Contains(got, "supplicant") {
		t.Errorf("network section %q goes by the supplicant", got)
	}
}
