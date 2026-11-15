//go:build !dot && !spot

package display

import (
	"reflect"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"golang.org/x/image/font/basicfont"
)

func TestJapaneseTitlesWrapWithoutLosingCharacters(t *testing.T) {
	r := testRenderer()
	f := basicfont.Face7x13 // Fixed widths make this independent of installed fonts.
	for _, wrap := range []struct {
		name string
		fn   func(string, int) []string
	}{
		{"dashboard", func(s string, w int) []string { return r.wrapLines(f, s, w) }},
		{"calendar", func(s string, w int) []string { return r.calWrap(f, s, w) }},
	} {
		for _, title := range []string{"明日の午後は家族と買い物に出かける予定です", "家族の予定Calendar2026を確認する", "か\u3099くせいのよていをかくにんする"} {
			lines := wrap.fn(title, 42)
			if len(lines) < 2 || strings.Join(lines, "") != title {
				t.Fatalf("%s lost or truncated %q: %q", wrap.name, title, lines)
			}
			for _, line := range lines {
				first, _ := utf8.DecodeRuneInString(line)
				if r.width(f, line) > 42 || !utf8.ValidString(line) || unicode.IsMark(first) {
					t.Errorf("%s: invalid line %q", wrap.name, line)
				}
			}
		}
		if got := wrap.fn("one two three", 49); !reflect.DeepEqual(got, []string{"one two", "three"}) {
			t.Errorf("%s changed English wrapping: %q", wrap.name, got)
		}
		if got := wrap.fn("", 42); len(got) != 0 {
			t.Errorf("%s: empty text: %q", wrap.name, got)
		}
		if got := wrap.fn("日本語", 1); len(got) != 3 {
			t.Errorf("%s: narrow width did not progress: %q", wrap.name, got)
		}
	}
	// Six characters fit; the 。 that would start the next line takes the です before it along.
	for _, line := range r.wrapLines(f, "今日は雨です。明日は晴れ", 42) {
		if strings.HasPrefix(line, "。") {
			t.Errorf("a line starts with 。: %q", r.wrapLines(f, "今日は雨です。明日は晴れ", 42))
		}
	}
	if got := r.calWrap(f, "Parent-teacher night", 49); !reflect.DeepEqual(got, []string{"Parent-", "teacher", "night"}) {
		t.Errorf("changed hyphen wrapping: %q", got)
	}
}

// A Japanese run after a Latin word goes on from it on the same line, cut between Japanese characters
// and not inside the Latin word: "Home" is not left alone on a line before "Assistantで…".
func TestJapaneseRunsOnAfterALatinWord(t *testing.T) {
	r := testRenderer()
	f := basicfont.Face7x13 // 7 pixels a character
	s := "Home Assistantで追加してください：設定の「デバイスとサービス」に表示されます。"
	lines := r.wrapLines(f, s, 7*20)
	if len(lines) < 2 || lines[0] == "Home" || !strings.HasPrefix(lines[0], "Home Assistantで") {
		t.Fatalf("the run did not go on from Home: %q", lines)
	}
	if got := strings.Join(lines, ""); got != s {
		t.Fatalf("characters lost: %q", lines)
	}
	for _, line := range lines {
		if r.width(f, line) > 7*20 {
			t.Errorf("%q is wider than the line", line)
		}
		if first, _ := utf8.DecodeRuneInString(line); strings.ContainsRune(noLineStart, first) {
			t.Errorf("%q starts with a closing mark", line)
		}
	}
	// Nor is a number or a Latin word inside a run cut in two.
	for _, width := range []int{7 * 14, 7 * 16, 7 * 18, 7 * 20} { // "192.168.1.20、" is 13 wide
		s := "ESPHomeを192.168.1.20、ポート6053、この鍵で追加してください："
		got := r.wrapLines(f, s, width)
		if strings.Join(got, "") != s {
			t.Fatalf("characters lost: %q", got)
		}
		for i := 1; i < len(got); i++ {
			end, _ := utf8.DecodeLastRuneInString(got[i-1])
			first, _ := utf8.DecodeRuneInString(got[i])
			if end < 0x80 && first < 0x80 && end != ' ' && first != ' ' {
				t.Errorf("width %d: %q cut between %q and %q", width, got, end, first)
			}
		}
	}
	// A run on its own still starts its own lines, as before.
	if got := r.wrapLines(f, "明日の午後は家族と買い物に出かける予定です", 42); strings.Join(got, "") != "明日の午後は家族と買い物に出かける予定です" {
		t.Errorf("lost characters: %q", got)
	}
}
