//go:build !dot

package display

import (
	"image"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// firstRunLanguages are the screen's languages first run is drawn in.
var firstRunLanguages = []string{"", "es", "fr", "gl", "it", "nl", "ja", "de", "tr"}

// previewLang is whether lang's first run is written out with a preview: English, a long language and
// one without spaces.
func previewLang(lang string) bool { return lang == "" || lang == "de" || lang == "ja" }

// langName is lang in a file's name.
func langName(lang string) string {
	if lang == "" {
		return "en"
	}
	return lang
}

// firstRunCases are the views of every step that draw different words, on the Show and the Spot.
func firstRunCases(at time.Time) map[string]firstRunView {
	joined := wifi.Status{Connected: true, SSID: "Example Network", Address: "192.168.1.20"}
	v := func(step string, set func(*firstRunView)) firstRunView {
		out := firstRunView{step: step, number: 5, of: 6, name: "Kitchen Show", wifi: joined, checked: true}
		if set != nil {
			set(&out)
		}
		return out
	}
	return map[string]firstRunView{
		"wifi-none":      v(frWifi, func(v *firstRunView) { v.wifi = wifi.Status{} }),
		"wifi":           v(frWifi, nil),
		"zone":           v(frZone, func(v *firstRunView) { v.phone = true }),
		"name":           v(frName, nil),
		"name-room":      v(frName, func(v *firstRunView) { v.room = "Living Room" }),
		"name-home":      v(frName, func(v *firstRunView) { v.home = true }),
		"name-renaming":  v(frName, func(v *firstRunView) { v.renaming = true }),
		"home":           v(frHome, func(v *firstRunView) { v.key = "Zm9yIHRoZSBmaXJzdCBydW4gdGVzdHMgb25seSEhIQ==" }),
		"home-key":       v(frHome, func(v *firstRunView) { v.key, v.showKey = "Zm9yIHRoZSBmaXJzdCBydW4gdGVzdHMgb25seSEhIQ==", true }),
		"home-adopt":     v(frHome, func(v *firstRunView) { v.adopt = at.Add(15 * time.Minute) }),
		"home-unkeyed":   v(frHome, func(v *firstRunView) { v.unkeyed = true }),
		"home-offline":   v(frHome, func(v *firstRunView) { v.wifi = wifi.Status{} }),
		"home-just":      v(frHome, func(v *firstRunView) { v.home, v.homeJust = true, true }),
		"home-connected": v(frHome, func(v *firstRunView) { v.home = true }),
		"updates-look":   v(frUpdates, func(v *firstRunView) { v.checked = false }),
		"updates-none":   v(frUpdates, func(v *firstRunView) { v.auto = true }),
		"updates-auto":   v(frUpdates, func(v *firstRunView) { v.offered, v.auto = "v1.7.0", true }),
		"updates-manual": v(frUpdates, func(v *firstRunView) { v.offered = "v1.7.0" }),
		"sound":          v(frSound, nil),
	}
}

// writeFirstRunPreview writes img to path, for a preview to look at.
func writeFirstRunPreview(t *testing.T, path string, img image.Image) {
	t.Helper()
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(out, img); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
