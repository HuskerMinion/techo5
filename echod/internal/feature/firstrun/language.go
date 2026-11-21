package firstrun

import (
	"fmt"
	"log/slog"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The language step: the screen's language (config.Screen.Language) and its clock, 12 or 24 hours
// (config.Screen.Clock24), first, so the rest of first run is in the words chosen. It shows what is set
// now, so a device the installer set up goes on with one tap, and saves only what is changed on it.

// Lang is one of the screen languages offered: its code, as the Language setting keeps it, and its
// name, in its own words.
type Lang struct{ Code, Name string }

// Languages are the screen's languages, every choice of the Language setting but Match all, which
// draws in English and is shown as English here.
var Languages = []Lang{
	{"en", "English"}, {"ca", "Valencià"}, {"de", "Deutsch"}, {"es", "Español"}, {"fr", "Français"}, {"gl", "Galego"},
	{"it", "Italiano"}, {"nl", "Nederlands"}, {"pt", "Português"}, {"ru", "Русский"}, {"tr", "Türkçe"},
	{"ja", "日本語"},
}

// LanguageNow is the language the step shows as chosen: the screen's, or English while it is unset
// (Match all), which is what the screen is drawn in then.
func LanguageNow() string {
	lang := config.Get().Screen.Language
	for _, l := range Languages {
		if l.Code == lang {
			return lang
		}
	}
	return "en"
}

// Clock24For is the clock lang's speakers usually read: 12 hours in English, 24 in the others.
func Clock24For(lang string) bool { return lang != "en" && lang != "" }

// SetLanguage makes lang the screen's language, and the clock the one it usually goes with, which the
// step can change after.
func SetLanguage(lang string) error {
	if err := config.Set().Screen().Language(lang); err != nil {
		return err
	}
	slog.Info("first run: screen language", "language", lang)
	return SetClock24(Clock24For(lang))
}

// SaveLanguage saves a language and a clock chosen together, each only if it changed: the clock is
// the one chosen, not lang's usual one, and a language left on English while it is unset (Match all)
// stays unset.
func SaveLanguage(lang string, clock24 bool) error {
	if lang != LanguageNow() {
		if err := config.Set().Screen().Language(lang); err != nil {
			return fmt.Errorf("could not save the language: %w", err)
		}
		slog.Info("first run: screen language", "language", lang)
	}
	if clock24 != config.Get().Screen.Clock24 {
		if err := SetClock24(clock24); err != nil {
			return fmt.Errorf("could not save the clock: %w", err)
		}
	}
	return nil
}

// SetClock24 saves the screen's clock as 24 hours, or 12, and tells the screen, which keeps it to
// draw from (clockChanged).
func SetClock24(on bool) error {
	if err := config.Set().Screen().Clock24(on); err != nil {
		return err
	}
	clockChanged(on)
	Changed()
	return nil
}

// clockChanged is the screen being told its clock changed, handed over by it (OnClock); nothing on a
// device with no screen.
var clockChanged = func(bool) {}

// OnClock is the screen giving first run what it does when the clock changes.
func OnClock(f func(on bool)) { clockChanged = f }
