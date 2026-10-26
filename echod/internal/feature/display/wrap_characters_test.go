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
	if got := r.calWrap(f, "Parent-teacher night", 49); !reflect.DeepEqual(got, []string{"Parent-", "teacher", "night"}) {
		t.Errorf("changed hyphen wrapping: %q", got)
	}
}
