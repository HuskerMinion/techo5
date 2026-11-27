//go:build !dot

package dashboard

import (
	"reflect"
	"testing"
)

// The card from #757 keeps its heading, its bold words, its bullets and the gap between its
// paragraphs, where it used to come out as four lines of the same plain text.
func TestMarkdownKeepsItsStructure(t *testing.T) {
	got := MarkText("## Today\n**Partly cloudy.** Light winds.\n\n- High 24°\n- Rain 20%\n")
	want := []TextLine{
		{Kind: "heading", Level: 2, Runs: []TextRun{{Text: "Today"}}},
		{Kind: "para", Runs: []TextRun{{Text: "Partly cloudy.", Bold: true}, {Text: " Light winds."}}},
		{Kind: "gap"},
		{Kind: "bullet", Runs: []TextRun{{Text: "High 24°"}}},
		{Kind: "bullet", Runs: []TextRun{{Text: "Rain 20%"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	// The card filling the screen alone still has the plain words.
	if p := plainText("## Today\n**Partly cloudy.** Light winds.\n\n- High 24°"); !reflect.DeepEqual(p,
		[]string{"Today", "Partly cloudy. Light winds.", "High 24°"}) {
		t.Fatalf("plain: %q", p)
	}
}

// Only marks in pairs are marks: snake_case and 2 * 3 keep what they have (#757's "while in there").
func TestMarkdownLeavesLoneMarks(t *testing.T) {
	for in, want := range map[string]string{
		"snake_case_name":        "snake_case_name",
		"2 * 3 = 6":              "2 * 3 = 6",
		"an *italic* word":       "an italic word",
		"an _italic_ word":       "an italic word",
		"~~gone~~ and `code`":    "gone and code",
		"[a link](http://x) <b>": "a link",
		"&amp; more":             "& more",
	} {
		if got := plainText(in); len(got) != 1 || got[0] != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

// A line bold throughout is one bold run; blank lines at the ends and in a row are one gap or none;
// a rule is a gap; a heading's closing hashes go, a C# in it stays.
func TestMarkdownLinesAndGaps(t *testing.T) {
	got := MarkText("\n\n**Warning**\n\n\n---\n# Kitchen #\n### C#\n* one\n+ two\n> quoted\n\n")
	want := []TextLine{
		{Kind: "para", Runs: []TextRun{{Text: "Warning", Bold: true}}},
		{Kind: "gap"},
		{Kind: "heading", Level: 1, Runs: []TextRun{{Text: "Kitchen"}}},
		{Kind: "heading", Level: 3, Runs: []TextRun{{Text: "C#"}}},
		{Kind: "bullet", Runs: []TextRun{{Text: "one"}}},
		{Kind: "bullet", Runs: []TextRun{{Text: "two"}}},
		{Kind: "para", Runs: []TextRun{{Text: "quoted"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	// Bold in the middle keeps its spaces on both sides.
	if r := inlineRuns("It is **very** windy"); !reflect.DeepEqual(r, []TextRun{{Text: "It is "}, {Text: "very", Bold: true}, {Text: " windy"}}) {
		t.Fatalf("runs: %+v", r)
	}
}

// Bold in underscores is bold too, and is no crash: its pair is the expression's second group, the first
// unset when it matches.
func TestUnderscoreBoldIsBold(t *testing.T) {
	if r := inlineRuns("__x__"); !reflect.DeepEqual(r, []TextRun{{Text: "x", Bold: true}}) {
		t.Fatalf("runs: %+v", r)
	}
	if r := inlineRuns("It is __very__ windy and **cold**"); !reflect.DeepEqual(r, []TextRun{{Text: "It is "}, {Text: "very", Bold: true}, {Text: " windy and "}, {Text: "cold", Bold: true}}) {
		t.Fatalf("runs: %+v", r)
	}
}

// A markdown card's block carries both: the marked lines it draws and the plain ones a lone card uses.
func TestMarkdownCardBlock(t *testing.T) {
	n := textNode{body: textRef{plain: "## Today\n- Rain 20%"}}
	b := n.blocks(look{})
	if len(b) != 1 || len(b[0].Marked) != 2 || b[0].Marked[0].Kind != "heading" || len(b[0].Text) != 2 {
		t.Fatalf("block: %+v", b)
	}
}
