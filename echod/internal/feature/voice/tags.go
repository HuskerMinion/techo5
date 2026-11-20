package voice

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// audioTag is a cue in square brackets that a text-to-speech engine takes as how to say what follows,
// not as words to say: ElevenLabs v3's [whispers] or [laughs], or a style such as [calm] that a Wyoming
// server turns into an instruction for its model. Home Assistant hands the screen the same text it hands
// the voice, tags and all. The spaces around a tag go with it. Brackets right before a "(" are a
// Markdown link's text, not a tag, and withoutTags keeps them (Go's regexp cannot look ahead).
var audioTag = regexp.MustCompile(`[ \t]*\[[^\[\]\n]{1,100}\][ \t]*`)

// withoutTags is a reply as it is shown, on the screen and in Home Assistant: its audio tags left out.
// Where a tag stood between two words one space stays; before punctuation, at the start or end of a line,
// or next to another tag, none does.
func withoutTags(text string) string {
	spans := audioTag.FindAllStringIndex(text, -1)
	if spans == nil {
		return text
	}
	var b strings.Builder
	last := 0
	for _, s := range spans {
		if link(text, s) {
			continue // the text up to it and the link itself go out with the next span or the end
		}
		b.WriteString(text[last:s[0]])
		last = s[1]
		out := b.String()
		if out == "" || strings.HasSuffix(out, "\n") || last == len(text) {
			continue
		}
		if next, _ := utf8.DecodeRuneInString(text[last:]); !strings.ContainsRune(".,!?;:…\n[", next) {
			b.WriteByte(' ')
		}
	}
	b.WriteString(text[last:])
	return strings.TrimSpace(b.String())
}

// link is whether the audioTag match s in text is a Markdown link's text, [the docs](https://...): its
// closing bracket followed straight away by an opening parenthesis.
func link(text string, s []int) bool {
	end := strings.LastIndexByte(text[s[0]:s[1]], ']') + s[0] + 1
	return end < len(text) && text[end] == '('
}
