//go:build !dot && !spot

package display

import (
	"bytes"
	"image"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
)

// skipLanguages are the languages the screen's setting offers, and English: "" is the language the
// screen starts in, and "" and "en" are both English. "xx" is a language with no text of its own, which
// draws English as every other line on the screen does (lib/locale). Short actions have catalog
// equivalents; feedback is translated in Spanish and otherwise retains English pending native review.
var skipLanguages = []string{"", "en", "es", "de", "fr", "it", "nl", "pt", "ru", "tr", "ja", "xx"}

// The page's own words are drawn in every one of those languages: the button's line and the foot's are
// whole messages with the alarm and the hour in them, and a language with only the English text draws
// that one, as the rest of the screen's text does.
func TestThePagesWordsInEveryScreenLanguage(t *testing.T) {
	now := time.Date(2026, time.October, 19, 6, 45, 0, 0, time.Local)
	at := now.Add(15 * time.Minute)
	offer := skipOffer{token: alarm.SkipToken{ID: "wake", At: at}, label: "Wake up"}
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	for _, lang := range skipLanguages {
		if err := config.Set().Screen().Language(lang); err != nil {
			t.Fatal(err)
		}
		action, done, refused := skipAction(), skipWords(offer, true), skipWords(offer, false)
		for _, line := range []string{action, done, refused} {
			if line == "" || strings.Contains(line, "%!") || strings.Contains(line, "%s") {
				t.Errorf("%q: %q is not a whole message", lang, line)
			}
			if day := claimedDay(line); day != "" {
				t.Errorf("%q: %q claims a day (%q)", lang, line, day)
			}
		}
		for _, line := range []string{done, refused} {
			if !strings.Contains(line, "Wake up") || !strings.Contains(line, clockText(at)) {
				t.Errorf("%q: %q does not name the alarm and the hour", lang, line)
			}
		}
	}
}

// An alarm's name has no length limit, and a name in another script is wider than an English one, so
// the button's second line is clipped for the name and not for the whole line: the hour is what says
// which occurrence a press is about, and it stays whole at both ends of the light on either panel, on
// a 12- or a 24-hour clock. The names here carry accents, Cyrillic and Japanese, which are the widths
// a translation brings.
func TestTheButtonKeepsTheHourUnderALongName(t *testing.T) {
	now := time.Date(2026, time.October, 19, 6, 45, 0, 0, time.Local)
	at := now.Add(15 * time.Minute)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	was := clock24.Load()
	t.Cleanup(func() { clock24.Store(was) })
	names := []string{
		strings.Repeat("La alarma de los niños para el colegio ", 3),
		strings.Repeat("Утренний будильник всей семьи ", 3),
		strings.Repeat("子どもたちを学校に送るためのアラーム", 3),
	}
	for _, panel := range showPanels() {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
		// The room a line of the pill has, as sunriseSkipButton measures it.
		wide := r.w - 2*r.margin - 2*r.s(48)
		for _, h24 := range []bool{false, true} {
			clock24.Store(h24)
			moment := " · " + clockText(at)
			for _, name := range names {
				if got := r.width(r.small, name+moment); got <= wide {
					t.Fatalf("%s: %q is %d wide in a pill of %d, not long enough to need the hour kept",
						panel.name, name, got, wide)
				}
				line := r.skipWhat(name, at, wide)
				if !strings.HasSuffix(line, moment) {
					t.Errorf("%s/24h=%v: the button says %q, without the hour %q",
						panel.name, h24, line, moment)
				}
				if got := r.width(r.small, line); got > wide {
					t.Errorf("%s/24h=%v: the button's line is %d wide, past the pill's %d",
						panel.name, h24, got, wide)
				}
			}
		}
	}

	// A line that fits is said whole, name and hour both: nothing is clipped for nothing.
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	wide := r.w - 2*r.margin - 2*r.s(48)
	if got, want := r.skipWhat("Wake up", at, wide), "Wake up · "+clockText(at); got != want {
		t.Errorf("a name that fits is drawn as %q, want %q", got, want)
	}
}

// Even when a font leaves no room for a name, the identifying time wins. If the
// time itself cannot fit, clipping it is more useful than showing only the name.
func TestTheTimeTakesPriorityWhenTheButtonIsNarrow(t *testing.T) {
	at := time.Date(2026, time.October, 19, 7, 0, 0, 0, time.Local)
	was := clock24.Load()
	t.Cleanup(func() { clock24.Store(was) })
	for _, panel := range showPanels() {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
		for _, h24 := range []bool{false, true} {
			clock24.Store(h24)
			moment := clockText(at)
			width := r.width(r.small, moment)
			for _, wide := range []int{width, max(1, width/2)} {
				if got, want := r.skipWhat("A very long alarm name", at, wide), r.clipTo(r.small, moment, wide); got != want {
					t.Errorf("%s/24h=%v/width=%d: got %q, want the time %q", panel.name, h24, wide, got, want)
				}
			}
		}
	}
}

// A long name must not push the result or the hour off the only answer a press gets: the foot fits the
// alarm's name to the room it has and keeps the rest of the catalog's sentence whole, on both panels,
// in both the foot's amber and the light's dark pill, on a 12- and a 24-hour clock, in the languages
// with words of their own and in the ones that draw English. The names here are Latin with accents,
// Cyrillic and Japanese, the widths a translation brings, and an unnamed alarm is named by the word for
// one rather than left out.
func TestTheFootKeepsTheResultAndHourWhenItFitsALongName(t *testing.T) {
	now := time.Date(2026, time.October, 19, 6, 59, 0, 0, time.Local)
	at := now.Add(time.Minute)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	was := clock24.Load()
	t.Cleanup(func() {
		clock24.Store(was)
		_ = config.Set().Screen().Language("")
	})
	families := append([]fonts.Family(nil), fonts.Families()...)
	families = append(families, fonts.Family{Name: "Antonio", Value: fonts.Antonio}, fonts.Family{Name: "C64", Value: fonts.C64})
	for _, family := range families {
		for _, panel := range showPanels() {
			r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
			r.makeFaces(fontKey{family.Value, fonts.Generation()})
			room := r.w - 2*r.margin
			pill := room - 2*r.s(14)
			for _, h24 := range []bool{false, true} {
				clock24.Store(h24)
				for _, lang := range []string{"", "es"} {
					if err := config.Set().Screen().Language(lang); err != nil {
						t.Fatal(err)
					}
					result := []string{"Alarm skipped:", "Not skipped:"}
					if lang == "es" {
						result = []string{"Alarma omitida:", "No se pudo omitir:"}
					}
					for _, name := range []string{
						"",
						strings.Repeat("La alarma de los niños para el colegio ", 12),
						strings.Repeat("Утренний будильник всей семьи ", 12),
						strings.Repeat("子どもたちを学校に送るためのアラーム", 12),
					} {
						for i, skipped := range []bool{true, false} {
							answer := skipAnswer{label: name, at: at, skipped: skipped}
							for _, wide := range []int{room, pill} {
								minimum := skipLineIn(skipFormat(skipped), "…", at)
								if got := r.width(r.tiny, minimum); got > wide {
									t.Fatalf("%s/%s/%q/24h=%v: fixed sentence %q is %d wide, past %d", family.Name, panel.name, lang, h24, minimum, got, wide)
								}
								line := r.skipLine(answer, r.tiny, wide)
								if got := r.width(r.tiny, line); got > wide {
									t.Errorf("%s/%q/24h=%v/wide=%d: %q is %d wide", panel.name, lang, h24, wide, line, got)
								}
								if !strings.Contains(line, clockText(at)) {
									t.Errorf("%s/%q/24h=%v/wide=%d: %q lost the hour %q",
										panel.name, lang, h24, wide, line, clockText(at))
								}
								if !strings.HasPrefix(line, result[i]) {
									t.Errorf("%s/%q/24h=%v/wide=%d: %q does not say %q first",
										panel.name, lang, h24, wide, line, result[i])
								}
							}
						}
					}
				}
			}
		}
	}
}

// The screen's own sentences fit the room the foot has even with no alarm named: the fitting above only
// ever shortens the name, and a catalog line that could not be drawn unnamed on a panel would be a
// sentence for nowhere (skipFitted keeps the whole line rather than cut its hour).
func TestTheFootsSentenceFitsEveryPanelUnnamed(t *testing.T) {
	at := time.Date(2026, time.October, 19, 6, 59, 0, 0, time.Local)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	was := clock24.Load()
	t.Cleanup(func() {
		clock24.Store(was)
		_ = config.Set().Screen().Language("")
	})
	for _, panel := range showPanels() {
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
		pill := r.w - 2*r.margin - 2*r.s(14)
		for _, h24 := range []bool{false, true} {
			clock24.Store(h24)
			for _, lang := range []string{"", "es"} {
				if err := config.Set().Screen().Language(lang); err != nil {
					t.Fatal(err)
				}
				for _, skipped := range []bool{true, false} {
					line := skipLineIn(skipFormat(skipped), "", at)
					if got := r.width(r.tiny, line); got > pill {
						t.Errorf("%s/%q/24h=%v: the unnamed answer %q is %d wide, past the pill's %d",
							panel.name, lang, h24, line, got, pill)
					}
				}
			}
		}
	}
}

// A language may name the alarm and the hour in its own order — %[2]s first, %[1]s second — and the
// line is the whole sentence filled in, never fragments put together: the fitting shortens the name
// where it is and leaves the rest, the result and the hour, where the language put them.
func TestTheFootsAnswerInAWholeReorderedSentence(t *testing.T) {
	at := time.Date(2026, time.October, 19, 7, 0, 0, 0, time.Local)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	was := clock24.Load()
	t.Cleanup(func() { clock24.Store(was) })
	const format = "%[2]s · %[1]s"
	for _, h24 := range []bool{false, true} {
		clock24.Store(h24)
		r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
		full := clockText(at) + " · Wake up"
		if got, want := skipLineIn(format, "Wake up", at), full; got != want {
			t.Errorf("24h=%v: the reordered sentence says %q, want %q", h24, got, want)
		}
		wide := r.width(r.tiny, full)
		if got := r.skipFitted(format, "Wake up", at, r.tiny, wide); got != full {
			t.Errorf("24h=%v: a line that fits is drawn as %q, want %q", h24, got, full)
		}
		long := strings.Repeat("Утренний будильник всей семьи ", 8)
		narrow := r.width(r.tiny, clockText(at)+" · …") + r.width(r.tiny, "Wake")
		line := r.skipFitted(format, long, at, r.tiny, narrow)
		if got := r.width(r.tiny, line); got > narrow {
			t.Errorf("24h=%v: the reordered line is %d wide, past its %d", h24, got, narrow)
		}
		if !strings.HasPrefix(line, clockText(at)+" · ") {
			t.Errorf("24h=%v: %q does not start with the hour where the language put it", h24, line)
		}
		if !strings.HasSuffix(line, "…") || strings.Contains(line, long) {
			t.Errorf("24h=%v: %q does not have the name fitted", h24, line)
		}
		if got, want := strings.Count(line, clockText(at)), 1; got != want {
			t.Errorf("24h=%v: %q names the hour %d times, want %d", h24, line, got, want)
		}
	}
}

// The pill is measured from the words in it, and the words are every language's: the button stays on
// the page, between the clock and the foot, with the longest action line and a long name, on both
// panels and on a 12- and a 24-hour clock — and a tap on it still finds the occurrence it drew.
func TestTheButtonStaysOnThePageInEveryLanguage(t *testing.T) {
	now := time.Date(2026, time.October, 19, 6, 45, 0, 0, time.Local)
	at := now.Add(15 * time.Minute)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	was := clock24.Load()
	t.Cleanup(func() {
		clock24.Store(was)
		_ = config.Set().Screen().Language("")
	})
	offer := skipOffer{token: alarm.SkipToken{ID: "wake", At: at},
		label: "Утренний будильник всей семьи и соседей"}
	for _, panel := range showPanels() {
		img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
		r := newRenderer(img)
		for _, h24 := range []bool{false, true} {
			clock24.Store(h24)
			for _, lang := range skipLanguages {
				if err := config.Set().Screen().Language(lang); err != nil {
					t.Fatal(err)
				}
				r.draw(scene{now: now, phase: "idle", sunrise: 0.4, skip: &offer})
				b := r.skipAt
				if b.Empty() {
					t.Fatalf("%s/%q/24h=%v: the button was not drawn", panel.name, lang, h24)
				}
				if b.Min.X < r.margin || b.Max.X > panel.wide-r.margin ||
					b.Min.Y < panel.high/2 || b.Max.Y > panel.high-r.s(48) {
					t.Errorf("%s/%q/24h=%v: the button at %v is not between the clock and the foot of a %dx%d page",
						panel.name, lang, h24, b, panel.wide, panel.high)
				}
				mid := b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2))
				if got, ok := r.skipTapped(mid); !ok || got.token != offer.token || got.label != offer.label {
					t.Errorf("%s/%q/24h=%v: a tap on the button at %v found %+v %v",
						panel.name, lang, h24, mid, got, ok)
				}
				// The two lines the pill was measured from. The action is elided where a language's
				// words are longer than the room there is, as every line on the screen is, and the pill
				// is measured from what is drawn; the moment stays whole beside it.
				wide := r.w - 2*r.margin - 2*r.s(48)
				t.Logf("%s/%q/24h=%v: %q · %q", panel.name, lang, h24,
					r.clipTo(r.title, skipAction(), wide), r.skipWhat(offer.label, at, wide))
			}
		}
	}
}

// The page's words are read when the frame is drawn, not when the page was first drawn: a language
// changed in the settings redraws the button in the new one, and the frame drawn in a language before
// it is the frame drawn in it again — nothing of the other language is kept, and the tap still finds
// the occurrence the frame drew.
func TestTheLanguageIsReadWhenTheFrameIsDrawn(t *testing.T) {
	now := time.Date(2026, time.October, 19, 6, 45, 0, 0, time.Local)
	at := now.Add(15 * time.Minute)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	offer := skipOffer{token: alarm.SkipToken{ID: "wake", At: at}, label: "Wake up"}
	for _, panel := range showPanels() {
		img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
		r := newRenderer(img)
		frame := func(lang string) ([]byte, image.Rectangle) {
			t.Helper()
			if err := config.Set().Screen().Language(lang); err != nil {
				t.Fatal(err)
			}
			r.draw(scene{now: now, phase: "idle", sunrise: 0.4, skip: &offer})
			return append([]byte(nil), img.Pix...), r.skipAt
		}

		spanish, spanishAt := frame("es")
		english, englishAt := frame("en")
		if bytes.Equal(spanish, english) {
			t.Errorf("%s: the page is drawn the same in Spanish and in English", panel.name)
		}
		again, againAt := frame("es")
		if !bytes.Equal(spanish, again) {
			t.Errorf("%s: the page drawn in Spanish again is not the page drawn in Spanish first", panel.name)
		}
		if spanishAt.Empty() || englishAt.Empty() || againAt.Empty() {
			t.Fatalf("%s: the button is not drawn in one of the languages", panel.name)
		}
		// The words are the screen's, not whatever the frame before them said.
		if err := config.Set().Screen().Language("es"); err != nil {
			t.Fatal(err)
		}
		if got, want := skipAction(), "Omitir"; got != want {
			t.Errorf("%s: the button says %q, want %q", panel.name, got, want)
		}
		// And the offer the frame published is the one a finger lands on, in either language.
		mid := againAt.Min.Add(image.Pt(againAt.Dx()/2, againAt.Dy()/2))
		if got, ok := r.skipTapped(mid); !ok || got.token != offer.token {
			t.Errorf("%s: a tap on the button at %v found %+v %v", panel.name, mid, got, ok)
		}
	}
}

// An alarm with no name of its own is named by the screen's word for one, in the language the foot is
// drawn in, as the button names it: a translated line with an English word in it would read as two.
func TestAnUnnamedAlarmIsNamedInTheScreensLanguage(t *testing.T) {
	now := time.Date(2026, time.October, 19, 6, 45, 0, 0, time.Local)
	offer := skipOffer{token: alarm.SkipToken{ID: "wake", At: now.Add(15 * time.Minute)}}
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	for _, tc := range []struct{ lang, word string }{
		{"", "Alarm"}, {"es", "Alarma"}, {"de", "Wecker"}, {"fr", "Réveil"}, {"it", "Sveglia"},
		{"nl", "Wekker"}, {"pt", "Alarme"}, {"ru", "Будильник"}, {"tr", "Alarm"}, {"ja", "アラーム"},
	} {
		if err := config.Set().Screen().Language(tc.lang); err != nil {
			t.Fatal(err)
		}
		if got := skipWords(offer, true); !strings.Contains(got, tc.word) {
			t.Errorf("%q: an unnamed alarm says %q, without %q", tc.lang, got, tc.word)
		}
	}
}

// The foot's answer is a whole translated message too, with an alarm's name of no length limit in it:
// it is clipped to the room beside the word at the right end, as it was, and stays on the page in the
// languages whose words are longest. It is the only answer a press gets, drawn dark on the palest end
// of the light, where a line that ran off the page would be gone with its hour.
func TestTheFootsAnswerIsOnThePageInEveryLanguage(t *testing.T) {
	now := time.Date(2026, time.October, 19, 6, 59, 0, 0, time.Local)
	at := now.Add(time.Minute)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	for _, panel := range showPanels() {
		for _, lang := range skipLanguages {
			if err := config.Set().Screen().Language(lang); err != nil {
				t.Fatal(err)
			}
			offer := skipOffer{token: alarm.SkipToken{ID: "wake", At: at},
				label: strings.Repeat("Утренний будильник всей семьи ", 4)}
			img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
			r := newRenderer(img)
			// The last minute of the light, the palest the sky under the line ever gets.
			r.draw(scene{now: now, phase: "idle", sunrise: 1, skipNote: answerOf(offer, true)})
			band := image.Rect(0, panel.high-r.s(64), panel.wide, panel.high)
			drawn := false
			for y := band.Min.Y; y < band.Max.Y; y++ {
				for x := band.Min.X; x < band.Max.X; x++ {
					if lightness(img.RGBAAt(x, y)) >= 0.05 {
						continue
					}
					drawn = true
					if x > panel.wide-r.margin {
						t.Errorf("%s/%q: the foot's answer reaches %d, past the margin at %d",
							panel.name, lang, x, panel.wide-r.margin)
					}
				}
			}
			if !drawn {
				t.Errorf("%s/%q: the foot's answer is not drawn on the light's page", panel.name, lang)
			}
		}
	}
}
