package firstrun

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A screen's first run asks for its language and clock right after the hello card, so the rest is in
// the words chosen; a device with no screen has neither to ask for.
func TestTheLanguageStepComesFirstOnAScreen(t *testing.T) {
	if screenless {
		if hasStep(Language) {
			t.Fatalf("a device with no screen asks for a screen language: %v", Steps())
		}
		return
	}
	if Steps()[1] != Language {
		t.Fatalf("the steps start %v, want the language after the hello card", Steps())
	}
	if n, _ := Number(Language); n != 1 {
		t.Errorf("the language step is number %d", n)
	}
}

// The step shows English while the screen still matches every language; choosing a language picks
// the clock it usually goes with, which can be changed after, and the screen is told.
func TestSetLanguagePicksItsClock(t *testing.T) {
	fresh(t)
	var told []bool
	was := clockChanged
	OnClock(func(on bool) { told = append(told, on) })
	t.Cleanup(func() { clockChanged = was })

	if got := LanguageNow(); got != "en" {
		t.Fatalf("an unset language is shown as %q", got)
	}
	steps := []struct {
		set     func() error
		lang    string
		clock24 bool
	}{
		{func() error { return SetLanguage("de") }, "de", true},
		{func() error { return SetClock24(false) }, "de", false},
		{func() error { return SetLanguage("en") }, "en", false},
		{func() error { return SetLanguage("ja") }, "ja", true},
	}
	for i, s := range steps {
		if err := s.set(); err != nil {
			t.Fatal(err)
		}
		c := config.Get().Screen
		if c.Language != s.lang || c.Clock24 != s.clock24 || LanguageNow() != s.lang {
			t.Errorf("%d: language %q, 24 hours %v; want %q, %v", i, c.Language, c.Clock24, s.lang, s.clock24)
		}
		if len(told) != i+1 || told[i] != s.clock24 {
			t.Errorf("%d: the screen was told %v", i, told)
		}
	}
}
