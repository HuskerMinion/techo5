//go:build !dot && !spot

package display

import (
	"image"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
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

// The drawer's rows are laid out on its panel, which is narrower than the settings card: a row there
// is as tall as the lines its words take in the panel's room and its button over its chip, so it does
// not run into the next row, and its words are whole there too.
func TestDrawerRowsAreAsTallAsTheirWordsInEveryLanguage(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	for _, lang := range firstRunLanguages {
		if err := config.Set().Screen().Language(lang); err != nil {
			t.Fatal(err)
		}
		for _, panel := range showPanels() {
			r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
			fc := r.faces()
			side := image.Rect(r.w-r.cardIn()-r.drawerW(), r.cardIn(), r.w-r.cardIn(), r.h-r.cardIn())
			// The radio twice: playing, and with a long problem under its error, which takes more lines in
			// the panel than it would on the settings card. A long camera name does the same.
			for i, tab := range []int{drawerCameras, drawerRadio, drawerRadio, drawerAnnounce, drawerCall} {
				radio := home.Radio{Configured: true, Playing: true, Now: "Station", Stations: []string{"Station"}}
				if i == 2 {
					radio = home.Radio{Configured: true, Problem: "The station directory did not answer in time, so the list of stations could not be loaded"}
				}
				s := scene{drawerTab: tab, announceReady: true, announcePeers: 3, radio: radio,
					cameras: []config.Camera{{Entity: "camera.door", Name: "Front door"},
						{Entity: "camera.yard", Name: "The camera above the garage door at the back of the yard"}},
					callees: []phone.Callee{{Name: "Kitchen", Device: true}}}
				rows, _ := drawerRows(s)
				for _, row := range rows {
					if row.kind == ctlHeading || row.kind == ctlSwatches || row.kind == ctlDays {
						continue
					}
					// The lines the words take in the panel's room, counted here apart from the row's layout.
					room := side.Max.X - r.s(26) - r.controlWidth(side, row) - r.s(rowGap) - (side.Min.X + r.rowIn())
					if row.locked {
						room -= r.s(lockGap + lockW)
					}
					need := r.rowH()
					if n := len(r.wrap(r.labelFace(row), row.label, room)); n > 1 {
						need += (min(n, 2) - 1) * r.labelFace(row).Metrics().Height.Ceil()
					}
					if n := len(r.wrap(fc.sub, row.sub, room)); row.sub != "" && n > 1 {
						need += (min(n, 3) - 1) * fc.sub.Metrics().Height.Ceil()
					}
					if r.stacked(side, row) {
						need += r.s(stackStep)
					}
					if h := r.rowHeight(side, row); h != need {
						t.Errorf("%s on the %s, drawer tab %d: %s is %d tall, its words need %d", lang, panel.name, tab, row.id, h, need)
					}
					if strings.HasPrefix(row.id, "cam:") {
						continue // a camera's name is the user's, of any length: two lines of it, then an ellipsis
					}
					labels, subs := r.wordLines(side, row)
					if got := strings.Join(labels, " "); labels != nil && got != row.label {
						t.Errorf("%s on the %s, drawer tab %d: the name %q is cut to %q", lang, panel.name, tab, row.label, got)
					}
					if got := strings.Join(subs, " "); subs != nil && got != row.sub {
						t.Errorf("%s on the %s, drawer tab %d: %s's line %q is cut to %q", lang, panel.name, tab, row.label, row.sub, got)
					}
				}
			}
		}
	}
}
