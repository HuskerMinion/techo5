//go:build !dot

package display

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/locale"
)

// wordsOption is one of the Words style's switches: how it says the time (locale.SayOptions).
type wordsOption struct {
	id       string // the settings row's
	objectID string // Home Assistant's
	name     string // Home Assistant's
	icon     string
	// german is set for a switch only German has a use for: its row shows only while the screen
	// speaks German. The switch in Home Assistant is always there.
	german bool
	// noRow keeps a switch off the settings sheet: it is set in Home Assistant and on the setup page.
	noRow bool
	get   func(config.Screen) bool
	set   func(on bool) error
}

// wordsOptionList are the switches, in the order the settings sheet lists them. Each is on when the
// style says it the way it always did, so a setting left alone keeps the screen as it was.
var wordsOptionList = []wordsOption{
	{
		id: "wordsregional", objectID: "screen_words_regional", name: "Words: regional wording", icon: "mdi:map-marker-radius",
		german: true,
		get:    func(s config.Screen) bool { return s.WordsRegional },
		set:    func(on bool) error { return config.Set().Screen().WordsRegional(on) },
	},
	{
		id: "wordsminute", objectID: "screen_words_every_minute", name: "Words: every minute", icon: "mdi:timer-outline",
		german: true,
		get:    func(s config.Screen) bool { return s.WordsEveryMinute },
		set:    func(on bool) error { return config.Set().Screen().WordsEveryMinute(on) },
	},
	{
		id: "wordsperiod", objectID: "screen_words_part_of_the_day", name: "Words: part of the day", icon: "mdi:weather-sunset",
		get: func(s config.Screen) bool { return !s.WordsNoPeriod },
		set: func(on bool) error { return config.Set().Screen().WordsNoPeriod(!on) },
	},
	{
		// The colors are a look rather than a way of saying the time: kept off the sheet, which has
		// enough rows under the style already (words_roles.go).
		id: "wordscolors", objectID: "screen_words_colors_by_role", name: "Words: colors by role", icon: "mdi:palette-outline",
		noRow: true,
		get:   func(s config.Screen) bool { return s.WordsColors },
		set:   func(on bool) error { return config.Set().Screen().WordsColors(on) },
	},
}

// wordsOptions are the switches as locale takes them.
func wordsOptions() locale.SayOptions {
	s := config.Get().Screen
	return locale.SayOptions{Regional: s.WordsRegional, EveryMinute: s.WordsEveryMinute, NoPeriod: s.WordsNoPeriod}
}

// wordsSwitches are the Home Assistant switches for the options; wake redraws the screen once one
// changes.
func wordsSwitches(wake func()) []*esphome.Switch {
	out := make([]*esphome.Switch, len(wordsOptionList))
	for i, o := range wordsOptionList {
		s := &esphome.Switch{Base: esphome.Base{ObjectID: o.objectID, Name: o.name, Icon: o.icon, Category: esphome.CategoryConfig}}
		s.OnCommand = func(on bool) {
			setWordsOption(s, o, on)
			wake()
		}
		out[i] = s
	}
	return out
}

// syncWordsSwitches shows Home Assistant the options as they are saved.
func syncWordsSwitches(sw []*esphome.Switch) {
	s := config.Get().Screen
	for i, o := range wordsOptionList {
		if i < len(sw) {
			sw[i].Set(o.get(s))
		}
	}
}

// setWordsOption saves one option and tells Home Assistant.
func setWordsOption(s *esphome.Switch, o wordsOption, on bool) {
	if err := o.set(on); err != nil {
		slog.Error("saving a setting failed", "setting", o.objectID, "err", err)
		s.Set(o.get(config.Get().Screen))
		return
	}
	s.Set(on)
	slog.Info("setting changed", "setting", o.objectID, "using", on)
}

// wordsRows are the options' settings rows, under the clock style while it is Words; the German
// ones only while the screen speaks German.
func wordsRows() []settingRow {
	if clockStyle() != "words" {
		return nil
	}
	s := config.Get().Screen
	var rows []settingRow
	for _, o := range wordsOptionList {
		if o.noRow || (o.german && screenLang() != "de") {
			continue
		}
		label, sub := wordsRowWords(o.id)
		rows = append(rows, settingRow{id: o.id, label: label, sub: sub, kind: ctlToggle, on: o.get(s)})
	}
	return rows
}

// setWordsColors turns the colors by role on or off from the setup page, telling Home Assistant.
func setWordsColors(sw []*esphome.Switch, on bool, wake func()) {
	for i, o := range wordsOptionList {
		if o.id == "wordscolors" && i < len(sw) {
			setWordsOption(sw[i], o, on)
		}
	}
	wake()
}

// wordsRowWords are an option's row's label and line under it, in the screen's language.
func wordsRowWords(id string) (label, sub string) {
	switch id {
	case "wordsregional":
		return tr("ui_words_regional"), tr("ui_words_regional_description")
	case "wordsminute":
		return tr("ui_words_every_minute"), tr("ui_words_every_minute_description")
	}
	return tr("ui_words_part_of_the_day"), tr("ui_words_part_of_the_day_description")
}

// wordsRowTap turns an option from its settings row. It reports whether id was one of them.
func wordsRowTap(sw []*esphome.Switch, id string, wake func()) bool {
	for i, o := range wordsOptionList {
		if o.id != id {
			continue
		}
		on := !o.get(config.Get().Screen)
		if i < len(sw) {
			setWordsOption(sw[i], o, on)
		} else if err := o.set(on); err != nil {
			slog.Error("saving a setting failed", "setting", o.objectID, "err", err)
		}
		wake()
		return true
	}
	return false
}
