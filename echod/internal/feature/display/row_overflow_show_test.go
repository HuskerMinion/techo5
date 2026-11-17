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

// A long name with no line under it, a radio station's in the drawer, is drawn on the lines it takes
// in the row grown for them, not on one cut line in its middle.
func TestALongNameWithNoLineUnderItTakesItsLines(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	for _, panel := range showPanels() {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
		side := image.Rect(r.w-r.cardIn()-r.drawerW(), r.cardIn(), r.w-r.cardIn(), r.h-r.cardIn())
		row := settingRow{id: "station:0", label: "Radio Bremen Eins Oldies und Schlager rund um die Uhr", kind: ctlButton, button: tr("play"), rowTap: true}
		labels, subs := r.wordLines(side, row)
		if len(labels) < 2 {
			t.Fatalf("%s: the name was meant to take two lines, it takes %d", panel.name, len(labels))
		}
		top, rh := 100, r.rowHeight(side, row)
		lines, _, _ := r.rowLines(top, rh, row, r.labelFace(row), labels, subs)
		var drawn []string
		for _, l := range lines {
			m := l.face.Metrics()
			if l.y-m.Ascent.Ceil() < top || l.y+m.Descent.Ceil() > top+rh {
				t.Errorf("%s: the line %q at %d runs out of the row %d..%d", panel.name, l.text, l.y, top, top+rh)
			}
			drawn = append(drawn, l.text)
		}
		if got := strings.Join(drawn, " "); got != row.label {
			t.Errorf("%s: the name is drawn as %q", panel.name, got)
		}
	}
}

// In a row grown for its words, a control sits in the row's middle, and the lower half of it is as
// much its tap target as the upper: the switch of a row tapped as a whole, a choice's button, a
// button and a danger button, and the days.
func TestAGrownRowsControlsAreTappedWhole(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	long := "A line under the name long enough to take three lines in the room beside any control on the row"
	for _, panel := range showPanels() {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
		card := r.nextCard(r.w, r.h)
		right := card.Max.X - r.s(26)
		cases := []struct {
			row  settingRow
			part part
			x    func(settingRow) int // where on the row the control is, across
		}{
			{settingRow{id: "t", label: "Toggle", sub: long, kind: ctlToggle, rowTap: true}, partMain, func(settingRow) int { return right - r.s(33) }},
			{settingRow{id: "b", label: "Button", sub: long, kind: ctlButton, button: "Go"}, partMain, func(settingRow) int { return right - r.s(20) }},
			{settingRow{id: "d", label: "Danger", sub: long, kind: ctlDanger, button: "Erase"}, partMain, func(settingRow) int { return right - r.s(20) }},
			{settingRow{id: "w", label: "Days", sub: long, kind: ctlDays, days: 1}, partDay, func(settingRow) int {
				w, gap := r.dayChip()
				return right - 7*w - 6*gap + w/2
			}},
		}
		for _, c := range cases {
			top := 100
			rh := r.rowHeight(card, c.row)
			if rh <= r.rowH() && c.row.kind != ctlDays {
				t.Fatalf("%s: %s did not grow", panel.name, c.row.id)
			}
			r.pending = nil
			r.settingRow(card, top, c.row)
			at := image.Pt(c.x(c.row), top+rh/2+r.s(15)) // the lower half of the control
			if z, ok := zoneIn(r.pending, at, 0); !ok || z.part != c.part {
				t.Errorf("%s: a tap on the lower half of %s's control (%v) misses it", panel.name, c.row.id, at)
			}
		}
	}
}

// A choice whose words take more lines stacks its button over its chip (a choice with a button never
// grows with the two beside each other): the lower half of the button runs the button, the lower half
// of the chip opens the choice.
func TestAStackedChoiceIsTappedWhere(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	for _, panel := range showPanels() {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
		card := r.nextCard(r.w, r.h)
		row := settingRow{id: "c", label: "Choice", kind: ctlChoice, value: "Home Assistant forecast", button: "Show",
			sub: "A line under the name long enough to take more lines in the room beside any control on the row"}
		if !r.stacked(card, row) {
			t.Fatalf("%s: the choice does not stack", panel.name)
		}
		top := 100
		cy := top + r.rowHeight(card, row)/2
		half := r.s(stackStep) / 2
		r.pending = nil
		r.settingRow(card, top, row)
		x := card.Max.X - r.s(26) - r.s(20)
		for _, c := range []struct {
			at   image.Point
			want part
		}{{image.Pt(x, cy-half+r.s(15)), partExtra}, {image.Pt(x, cy+half+r.s(15)), partMain}} {
			if z, ok := zoneIn(r.pending, c.at, 0); !ok || z.part != c.want {
				t.Errorf("%s: a tap at %v is part %d (found %v), want %d", panel.name, c.at, z.part, ok, c.want)
			}
		}
	}
}
