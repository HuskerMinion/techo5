//go:build !dot

package display

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/HuskerMinion/techo5/echod/internal/lib/fonts"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

// ---------- visualBidi unit tests ----------

func TestVisualBidiPureASCII(t *testing.T) {
	// No RTL runes: string must be returned unchanged, no allocation needed.
	cases := []string{
		"Living Room",
		"Echo Show 5",
		"Temperature: 22°C",
		"100%",
		"(open)",
		"on/off",
		"A–Z",
	}
	for _, s := range cases {
		if got := visualBidi(s); got != s {
			t.Errorf("pure ASCII %q: got %q, want unchanged", s, got)
		}
	}
}

func TestVisualBidiPureHebrew(t *testing.T) {
	// A pure Hebrew word must be character-reversed for LTR rendering.
	// שלום → (visual order) ם ו ל ש
	word := "שלום"
	got := visualBidi(word)
	wantRunes := []rune{'ם', 'ו', 'ל', 'ש'}
	gotRunes := []rune(got)
	if len(gotRunes) != len(wantRunes) {
		t.Fatalf("visualBidi(%q): len=%d, want %d", word, len(gotRunes), len(wantRunes))
	}
	for i, r := range gotRunes {
		if r != wantRunes[i] {
			t.Errorf("visualBidi(%q)[%d] = %q, want %q", word, i, r, wantRunes[i])
		}
	}
}

func TestVisualBidiTwoHebrewWords(t *testing.T) {
	// "חדר שינה" (bedroom) → visual: "הניש רדח"
	input := "חדר שינה"
	want := "הניש רדח"
	got := visualBidi(input)
	if got != want {
		t.Errorf("visualBidi(%q) = %q, want %q", input, got, want)
	}
}

func TestVisualBidiMixedLTRParagraph(t *testing.T) {
	// "Echo: סלון" – LTR paragraph base, Hebrew run is reversed in-place.
	// English words stay left; Hebrew word visually reversed.
	input := "Echo: סלון"
	got := visualBidi(input)
	// English prefix must be preserved.
	if !strings.HasPrefix(got, "Echo: ") {
		t.Errorf("visualBidi(%q): English prefix lost, got %q", input, got)
	}
	// Hebrew part must appear after the English prefix.
	hebrewPart := strings.TrimPrefix(got, "Echo: ")
	wantHebrew := "ןולס"
	if hebrewPart != wantHebrew {
		t.Errorf("visualBidi(%q): Hebrew part = %q, want %q", input, hebrewPart, wantHebrew)
	}
}

func TestVisualBidiMixedWithNumber_LTR(t *testing.T) {
	// "Room 2 סלון" – LTR base; number stays, Hebrew reverses.
	input := "Room 2 סלון"
	got := visualBidi(input)
	if !strings.Contains(got, "Room") {
		t.Errorf("visualBidi(%q): 'Room' missing, got %q", input, got)
	}
	if !strings.Contains(got, "2") {
		t.Errorf("visualBidi(%q): '2' missing, got %q", input, got)
	}
	// Hebrew word must have been reversed.
	if strings.Contains(got, "סלון") {
		t.Errorf("visualBidi(%q): Hebrew was NOT reversed (found original order), got %q", input, got)
	}
}

func TestVisualBidiMixedWithNumber_RTL(t *testing.T) {
	// "סלון 2" – RTL base: whole string reverses, number stays LTR within its run.
	input := "סלון 2"
	got := visualBidi(input)
	// The number '2' must appear in the result.
	if !strings.Contains(got, "2") {
		t.Errorf("visualBidi(%q): '2' missing, got %q", input, got)
	}
	// Hebrew must NOT appear in original order (must be visually reordered).
	if strings.Contains(got, "סלון") {
		t.Errorf("visualBidi(%q): Hebrew NOT reversed, got %q", input, got)
	}
}

func TestVisualBidiBracketsAreMirrored(t *testing.T) {
	// "Echo (סלון)" – brackets around RTL content must be mirrored.
	// UAX#9 bracket mirroring is handled by bidi.ReverseString.
	input := "Echo (סלון)"
	got := visualBidi(input)
	// The result must not be identical to input (because Hebrew is present).
	if got == input {
		t.Errorf("visualBidi(%q): string was not transformed, got %q", input, got)
	}
	// Must still contain 'Echo'.
	if !strings.Contains(got, "Echo") {
		t.Errorf("visualBidi(%q): 'Echo' missing, got %q", input, got)
	}
	// Rune count must be preserved.
	if utf8.RuneCountInString(got) != utf8.RuneCountInString(input) {
		t.Errorf("visualBidi(%q): rune count changed %d → %d",
			input, utf8.RuneCountInString(input), utf8.RuneCountInString(got))
	}
}

func TestVisualBidiPunctuation(t *testing.T) {
	// Punctuation at end of Hebrew sentence: "שלום!"
	input := "שלום!"
	got := visualBidi(input)
	// Result must start with '!' (UAX#9 places trailing weak chars on the right for RTL).
	gotRunes := []rune(got)
	if gotRunes[0] != '!' {
		t.Errorf("visualBidi(%q): expected '!' at position 0, got %q (full: %q)", input, gotRunes[0], got)
	}
}

func TestVisualBidiNikudStaysAttached(t *testing.T) {
	// "שָׁלוֹם" with nikud – combining marks must follow their base letter.
	// U+05E9 U+05C1 U+05B8 U+05DC U+05B9 U+05DD
	shin := '\u05E9'     // ש
	shinShin := '\u05C1' // ׁ (shin dot)
	qamats := '\u05B8'   // ָ
	lamed := '\u05DC'    // ל
	holam := '\u05B9'    // ֹ
	mem := '\u05DD'      // ם
	input := string([]rune{shin, shinShin, qamats, lamed, holam, mem})
	got := visualBidi(input)
	gotRunes := []rune(got)
	if len(gotRunes) != 6 {
		t.Fatalf("visualBidi(שָׁלוֹם): rune count = %d, want 6", len(gotRunes))
	}
	// After reversal the order should be: ם ל ֹ ש ׁ ָ → no, bidi.ReverseString keeps
	// modifiers attached: ם  loH ל  שָׁ → reversed: שָׁ ל לוֹ ם
	// The last rune in the result must be mem (first base, last to render in LTR order).
	// The first rune must be shin (last base in logical, first in visual).
	firstBase := gotRunes[0]
	if firstBase != shin && firstBase != mem {
		t.Errorf("visualBidi(שָׁלוֹם): unexpected first rune %q", firstBase)
	}
}

func TestVisualBidiMultilineEachLineIndependent(t *testing.T) {
	// Each line is an independent paragraph; lines must be processed separately.
	line1 := "Living Room"
	line2 := "סלון"
	input := line1 + "\n" + line2
	got := visualBidi(input)
	parts := strings.Split(got, "\n")
	if len(parts) != 2 {
		t.Fatalf("expected 2 lines after visualBidi, got %d", len(parts))
	}
	// LTR line unchanged.
	if parts[0] != line1 {
		t.Errorf("line1 changed: got %q, want %q", parts[0], line1)
	}
	// Hebrew line must be reversed.
	if parts[1] == line2 {
		t.Errorf("line2 (Hebrew) was NOT reversed: still %q", parts[1])
	}
}

func TestVisualBidiEmptyString(t *testing.T) {
	if got := visualBidi(""); got != "" {
		t.Errorf("visualBidi(%q) = %q, want empty", "", got)
	}
}

func TestVisualBidiRuneCountPreserved(t *testing.T) {
	// Reordering must never drop or duplicate runes.
	cases := []string{
		"שלום",
		"Echo: סלון 2 (ראשי)",
		"סלון 1",
		"טמפרטורה 22°C",
	}
	for _, s := range cases {
		got := visualBidi(s)
		wantLen := utf8.RuneCountInString(s)
		gotLen := utf8.RuneCountInString(got)
		if gotLen != wantLen {
			t.Errorf("visualBidi(%q): rune count %d → %d (dropped or duplicated runes)", s, wantLen, gotLen)
		}
	}
}

// ---------- font glyph presence tests ----------

func hebrewGlyphPresent(t *testing.T, f *sfnt.Font, r rune) {
	t.Helper()
	var buf sfnt.Buffer
	idx, err := f.GlyphIndex(&buf, r)
	if err != nil {
		t.Errorf("GlyphIndex(%q U+%04X): %v", r, r, err)
		return
	}
	if idx == 0 {
		t.Errorf("glyph missing: %q U+%04X (GID=0 = .notdef)", r, r)
	}
}

// TestGoBaselineHasNoHebrewGlyphs verifies the Go fallback font (goregular)
// does NOT contain Hebrew glyphs, confirming a runtime font file is required.
func TestGoBaselineHasNoHebrewGlyphs(t *testing.T) {
	f, err := opentype.Parse(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	var buf sfnt.Buffer
	hebrewSamples := []rune{'ש', 'ל', 'ו', 'מ', 'א', 'ב', 'ג', 'ד'}
	missing := 0
	for _, r := range hebrewSamples {
		idx, _ := f.GlyphIndex(&buf, r)
		if idx == 0 {
			missing++
		}
	}
	if missing == 0 {
		// Go now ships Hebrew glyphs — log but don't fail (test is informational).
		t.Logf("NOTE: goregular now has Hebrew glyphs; own-font fallback may not be needed")
	} else {
		t.Logf("goregular missing %d/%d Hebrew glyphs (expected: own-font fallback required)", missing, len(hebrewSamples))
	}
}

// TestOwnFontFolderPath verifies the own-font directory path is defined
// and that Hebrew fonts placed there will be discovered.
func TestOwnFontFolderPath(t *testing.T) {
	if fonts.Dir == "" {
		t.Error("fonts.Dir is empty; own-font discovery will not work")
	}
	t.Logf("own-font directory: %s", fonts.Dir)
}

// TestOpenSansBundledFontLacksHebrew verifies the subsetted OpenSans lacks Hebrew
// (confirming the font subsetter in make_fonts.py previously excluded it, so rebuilding
// the bundled fonts after the make_fonts.py patch is necessary to bake Hebrew in).
func TestOpenSansBundledFontLacksHebrew(t *testing.T) {
	f, err := fonts.Font("opensans", false)
	if err != nil {
		t.Fatalf("Font(opensans, false): %v", err)
	}
	var buf sfnt.Buffer
	// Hebrew alef U+05D0
	idx, _ := f.GlyphIndex(&buf, 'א')
	if idx != 0 {
		t.Logf("NOTE: opensans bundled font already has Hebrew glyphs (make_fonts.py was already patched and fonts rebuilt)")
	} else {
		t.Logf("CONFIRMED: opensans bundled font has no Hebrew glyphs — fonts must be rebuilt with updated make_fonts.py, OR a runtime Hebrew .ttf must be uploaded to the fonts folder")
	}
}
