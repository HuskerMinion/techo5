package fonts

import (
	"os"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func cutAt(word string, breaks []int) []string {
	var out []string
	start := 0
	for _, b := range breaks {
		out = append(out, word[start:b])
		start = b
	}
	return append(out, word[start:])
}

func TestSyllableBreaks(t *testing.T) {
	// the coeng (្) keeps រ under ត; vowel signs stay with their base; digits are their own piece
	word := "ម៉ាស៊ីនត្រជាក់25អង្សា។"
	got := strings.Join(cutAt(word, SyllableBreaks(word)), "|")
	if want := "ម៉ា|ស៊ី|ន|ត្រ|ជា|ក់|25|អ|ង្សា។"; got != want {
		t.Fatalf("syllables = %s, want %s", got, want)
	}
}

func TestLineBreaksWords(t *testing.T) {
	for in, want := range map[string]string{
		"ម៉ាស៊ីនត្រជាក់បើកហើយ":  "ម៉ាស៊ីនត្រជាក់|បើក|ហើយ",
		"ឡានសាកថ្មបានហើយ":       "ឡាន|សាក|ថ្ម|បាន|ហើយ",
		"កំដៅក្នុងផ្ទះ26អង្សា។": "កំដៅ|ក្នុង|ផ្ទះ|26|អង្សា។",
	} {
		if got := strings.Join(cutAt(in, LineBreaks(in)), "|"); got != want {
			t.Errorf("%s: words = %s, want %s", in, got, want)
		}
	}
}

func TestLineBreaksNone(t *testing.T) {
	for _, w := range []string{"hello", "25", "AC", "។", "ក"} {
		if b := LineBreaks(w); len(b) != 0 || HasLineBreaks(w) {
			t.Errorf("%q: breaks %v", w, b)
		}
	}
	if b := SyllableBreaks("ab​cd"); len(b) != 1 || b[0] != 5 {
		t.Errorf("zwsp breaks = %v", b)
	}
}

func TestSplitToWidth(t *testing.T) {
	runes := func(s string) int { return utf8.RuneCountInString(s) } // 1 unit per rune
	word := "ឡានសាកថ្មបានហើយ"                                        // ឡាន=3 សាក=3 ថ្ម=3 បាន=3 ហើយ=3 runes
	parts := SplitToWidth(word, 7, runes)
	if got := strings.Join(parts, "|"); got != "ឡានសាក|ថ្មបាន|ហើយ" {
		t.Errorf("by word = %s", got)
	}
	word = "ម៉ាស៊ីនត្រជាក់បើកហើយ" // one 14-rune word, then បើក=3 ហើយ=3
	// a word wider than the line is cut between its syllables
	parts = SplitToWidth(word, 5, runes)
	if strings.Join(parts, "") != word {
		t.Fatalf("pieces %q do not rebuild the word", parts)
	}
	for _, p := range parts {
		if runes(p) > 5 {
			t.Errorf("piece %q wider than 5", p)
		}
	}
	if SplitToWidth(word, 100, runes) != nil {
		t.Error("a word that fits is not split")
	}
	if SplitToWidth("helloworld", 3, runes) != nil {
		t.Error("Latin is never split")
	}
	parts = SplitToWidths(word, 3, 10, runes)
	if runes(parts[0]) > 3 || strings.Join(parts, "") != word {
		t.Errorf("first-width pieces %q", parts)
	}
	// a syllable wider than the line overflows on its own rather than looping
	parts = SplitToWidth("ស្ត្រីស្ត្រី", 1, runes)
	if strings.Join(parts, "") != "ស្ត្រីស្ត្រី" || len(parts) != 2 {
		t.Errorf("oversized syllables %q", parts)
	}
}

// TestLineBreaksOwnDict segments with a word list of the owner's own when TECHO5_KHMER_WORDS names
// one (the 44k khmer_segmenter list, say), printing what it makes of a few replies.
func TestLineBreaksOwnDict(t *testing.T) {
	path := os.Getenv("TECHO5_KHMER_WORDS")
	if path == "" {
		t.Skip("TECHO5_KHMER_WORDS not set")
	}
	DictPath, dictOnce = path, sync.Once{}
	if path == "builtin" {
		DictPath = "/nonexistent"
	}
	t.Cleanup(func() { dictOnce = sync.Once{} })
	for _, s := range []string{
		"ម៉ាស៊ីនត្រជាក់បានបើកហើយកំដៅ25អង្សា",
		"ឡានរបស់បងនៅសល់ថ្ម96ភាគរយហើយកំពុងសាកនៅផ្ទះ",
		"សីតុណ្ហភាពក្នុងបន្ទប់គឺ26.1អង្សាសេ និងសំណើម59ភាគរយ។",
	} {
		t.Log(strings.Join(cutAt(s, LineBreaks(s)), "|"))
	}
}
