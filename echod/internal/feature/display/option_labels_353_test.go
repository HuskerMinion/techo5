//go:build !dot

package display

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// The timer style, call ring and alarm sound pickers show the screen's language; the saved values
// and the Home Assistant options stay English (#353).
func TestTimerStyleAndRingLabelsFollowTheScreenLanguage(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	cases := []struct {
		lang   string
		styles []string
		rings  []string
		chimes string
	}{
		{"it", []string{"Riga", "Anello", "In alto a destra", "Riquadri", "Grande"}, []string{"Telefono tradizionale", "Campanello"}, "Carillon"},
		{"ja", []string{"1行", "リング", "隅", "バナー", "大"}, []string{"昔の電話", "チャイム"}, "チャイム音"},
		{"de", []string{"Zeile", "Ring", "Ecke", "Banner", "Groß"}, []string{"Altes Telefon", "Glocke"}, "Glockenspiel"},
		{"fr", []string{"Ligne", "Anneau", "Coin", "Bandeau", "Grand"}, []string{"Vieux téléphone", "Carillon"}, "Carillons"},
		{"tr", []string{"Satır", "Halka", "Köşe", "Şerit", "Büyük"}, []string{"Eski telefon", "Zil"}, "Çan sesleri"},
		{"en", []string{"Line", "Ring", "Corner", "Banner", "Big"}, []string{"Old phone", "Chime"}, "Chimes"},
	}
	for _, c := range cases {
		if err := config.Set().Screen().Language(c.lang); err != nil {
			t.Fatal(err)
		}
		if p, ok := pickerFor("timerstyle", sheetView{}); !ok || !reflect.DeepEqual(p.opts, c.styles) {
			t.Errorf("%s: timer styles %v", c.lang, p.opts)
		}
		if p, ok := pickerFor("callring", sheetView{}); !ok || !reflect.DeepEqual(p.opts, c.rings) {
			t.Errorf("%s: call rings %v", c.lang, p.opts)
		}
		p, ok := pickerFor("alarmsound", sheetView{})
		if !ok || len(p.opts) < 3 || p.opts[2] != c.chimes {
			t.Errorf("%s: alarm sounds %v", c.lang, p.opts)
		}
		// Every sound in the list, not some of it: a picker half in English reads as a mistake.
		if c.lang != "en" {
			for i, name := range speaker.AlarmSounds() {
				if name != "Home Assistant" && i < len(p.opts) && p.opts[i] == name { // a product name stays
					t.Errorf("%s: alarm sound %q is not translated", c.lang, name)
				}
			}
		}
	}
	// What is saved and what Home Assistant offers does not change with the language.
	if !reflect.DeepEqual(timerStyleOptions(), []string{"Line", "Ring", "Corner", "Banner", "Big"}) {
		t.Errorf("timer style options changed: %v", timerStyleOptions())
	}
	if !reflect.DeepEqual(phone.RingSounds, []string{"Old phone", "Chime"}) {
		t.Errorf("call rings changed: %v", phone.RingSounds)
	}
	if got := speaker.AlarmSounds()[2]; got != "Chimes" {
		t.Errorf("alarm sound changed: %q", got)
	}
}

// One photo and one folder read in the singular.
func TestPhotoPickerCounts(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	for lang, want := range map[string]string{
		"en": "Use this folder · 1 photo + 2 folders",
		"it": "Usa questa cartella · 1 foto + 2 cartelle",
	} {
		if err := config.Set().Screen().Language(lang); err != nil {
			t.Fatal(err)
		}
		p := folderPicker(folderView{photos: 1, folders: make([]hass.Media, 2)}, false)
		if p.opts[0] != want {
			t.Errorf("%s: %q, want %q", lang, p.opts[0], want)
		}
	}
}
