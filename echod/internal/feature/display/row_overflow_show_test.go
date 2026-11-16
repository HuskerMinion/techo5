//go:build !dot && !spot

package display

import (
	"image"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// On every Show panel and in every screen language, a settings row's name and the line under it are
// drawn whole: the line under it takes a second line rather than give up its end, and a choice with a
// button stacks the two when its words would not fit beside them (the Weather row in German).
func TestSettingsRowWordsAreWholeInEveryLanguage(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	for _, lang := range firstRunLanguages {
		if err := config.Set().Screen().Language(lang); err != nil {
			t.Fatal(err)
		}
		for _, panel := range showPanels() {
			r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
			card := r.nextCard(r.w, r.h)
			fc := r.faces()
			for cat := catDisplay; cat <= catGeneral; cat++ {
				st := settings{cat: cat, name: "Kitchen", weather: tr("weather_ha_forecast"), version: "v0.0.0-dev", slot: "a",
					wifiName: "Home", address: "192.168.1.50", wifiOK: true}
				rows, _ := categoryRows(sheetView{st: st})
				for _, row := range rows {
					if row.kind == ctlHeading || row.kind == ctlSwatches || row.kind == ctlDays {
						continue
					}
					label, sub, _ := r.rowWords(card, row, fc.label)
					labels, subs := r.wordLines(card, row)
					if labels != nil {
						label = strings.Join(labels, " ")
					}
					if label != row.label {
						t.Errorf("%s on the %s: the name %q is cut to %q", lang, panel.name, row.label, label)
					}
					if subs != nil {
						if got := strings.Join(subs, " "); got != row.sub {
							t.Errorf("%s on the %s: %s's line %q is cut to %q", lang, panel.name, row.label, row.sub, got)
						}
					} else if sub != row.sub {
						t.Errorf("%s on the %s: %s's line %q is cut to %q", lang, panel.name, row.label, row.sub, sub)
					}
				}
			}
		}
	}
}
