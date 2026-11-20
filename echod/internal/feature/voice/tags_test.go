package voice

import "testing"

// A reply's audio tags tell the voice how to speak and are left out of what is shown, with the spaces
// around them, so the words read as they are said.
func TestWithoutTags(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"The light is off.", "The light is off."},
		{"[calm] Good morning. It is seven.", "Good morning. It is seven."},
		{"Good morning. [cheerful] The sun is out!", "Good morning. The sun is out!"},
		{"[calm][friendly] Good night.", "Good night."},
		{"[calm] [friendly] Good night.", "Good night."},
		{"That was funny [laughs].", "That was funny."},
		{"Wait [whispers] the baby sleeps", "Wait the baby sleeps"},
		{"Hallo [stil: leicht ironisch, langsam] na super…", "Hallo na super…"},
		{"Done. [normal]", "Done."},
		{"[serious] Careful!\n[calm] All good.", "Careful!\nAll good."},
		{"Wow [laughs]… really", "Wow… really"},
		{"[sighs]", ""},
		{"An array [1, 2] stays [", "An array stays ["},
		{"It was cloudy [1] and cool [2].", "It was cloudy and cool."}, // citations go, as tags do
		// A Markdown link's text is the link's, not a tag: it stays, and a tag beside it still goes.
		{"See [the docs](https://example.com) for more.", "See [the docs](https://example.com) for more."},
		{"[calm] See [the docs](https://example.com) [laughs].", "See [the docs](https://example.com)."},
		{"[OK](https://example.com)", "[OK](https://example.com)"},
		{"Look [here] (not a link) too", "Look (not a link) too"},
	} {
		if got := withoutTags(c.in); got != c.want {
			t.Errorf("withoutTags(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
