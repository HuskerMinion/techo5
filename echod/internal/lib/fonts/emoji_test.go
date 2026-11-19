package fonts

import (
	"testing"

	"golang.org/x/image/font"
)

// fresh is a test with no emoji missed yet, and no fonts of one's own.
func fresh(t *testing.T) {
	t.Helper()
	ownDir(t)
	Rescan()
	emojiMissed.Store(false)
	t.Cleanup(func() { emojiMissed.Store(false) })
}

// An emoji drawn in Go alone, the default, is noticed, so the emoji font can be fetched; letters and
// Go's own symbols are not.
func TestAnEmojiInGoAloneIsNoticed(t *testing.T) {
	fresh(t)
	f, err := Face(Go, false, 20)
	if err != nil {
		t.Fatal(err)
	}
	font.MeasureString(f, "Dentist at 3 · ♪ ←")
	if EmojiMissed() {
		t.Fatal("letters and symbols Go has were taken for a missing emoji")
	}
	font.MeasureString(f, "Dentist 🦷")
	if !EmojiMissed() {
		t.Error("an emoji Go has no glyph for was not noticed")
	}
}

// The same in a chosen font, where the screen draws from a stack.
func TestAnEmojiInAChosenFontIsNoticed(t *testing.T) {
	fresh(t)
	f, err := Face("inter", false, 20)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.(*Stack); !ok {
		t.Fatalf("Inter's face is %T, not a stack", f)
	}
	font.MeasureString(f, "Party")
	if EmojiMissed() {
		t.Fatal("letters were taken for a missing emoji")
	}
	font.MeasureString(f, "Party 🎉")
	if !EmojiMissed() {
		t.Error("an emoji no layer has was not noticed")
	}
}

// A skin tone is drawn as nothing in both, not as a swatch or a box after the hand it colors.
func TestASkinToneTakesNoRoom(t *testing.T) {
	fresh(t)
	for _, value := range []string{Go, "inter"} {
		f, err := Face(value, false, 20)
		if err != nil {
			t.Fatal(err)
		}
		if with, without := font.MeasureString(f, "ok 👍🏽"), font.MeasureString(f, "ok 👍"); with != without {
			t.Errorf("%q: the skin tone took %v", value, with-without)
		}
		if adv, ok := f.GlyphAdvance(0x1F3FD); !ok || adv != 0 {
			t.Errorf("%q: a skin tone's advance is %v, %v", value, adv, ok)
		}
	}
}
