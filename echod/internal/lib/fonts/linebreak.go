package fonts

import (
	"bufio"
	"bytes"
	_ "embed"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// Khmer is written without spaces between words, so a reply in Khmer is often a single
// space-separated "word" wider than the screen. These helpers find where such a run may break: between
// dictionary words, and failing that between orthographic syllables, the smallest unit a Khmer line
// may be cut at without tearing a stacked consonant (coeng) or a vowel sign from its base. A
// zero-width space, which some text carries between Khmer words, is a break too.
//
// Words are found the way Sovichea/khmer_segmenter (MIT) does it: a Viterbi pass over the syllables
// that picks the cheapest run of dictionary words, cost being -log10 of a word's corpus share. The
// built-in list is the frequency word list SIL made for Khmer line breaking (khmerdict/, MIT); an
// owner may put a larger one at DictPath.

const (
	coeng = '្' // joins the next consonant under the previous one: no break before it
	zwsp  = '​'
)

// khmerBase is a consonant or independent vowel: the rune a Khmer syllable starts with.
func khmerBase(c rune) bool { return c >= 'ក' && c <= 'ឳ' }

// khmer is any rune of the Khmer block, punctuation and digits included.
func khmer(c rune) bool { return c >= 'ក' && c <= '៿' || c >= '᧠' && c <= '᧿' }

// syllableStart reports whether a syllable may begin at c, after prev: at a Khmer base not stacked by
// a coeng, after a zero-width space, and where Khmer meets other text (digits, Latin) either way.
// Marks and Khmer punctuation (។) never begin one.
func syllableStart(prev, c rune) bool {
	switch {
	case prev == zwsp:
		return true
	case khmerBase(c):
		return prev != coeng
	case unicode.IsMark(c) || c == zwsp:
		return false
	case khmer(prev) && !khmer(c):
		return true
	}
	return false
}

// SyllableBreaks is the byte offsets inside word (never 0 or len(word)) where a syllable begins.
func SyllableBreaks(word string) []int {
	var out []int
	prev := rune(-1)
	for i, c := range word {
		if i > 0 && syllableStart(prev, c) {
			out = append(out, i)
		}
		prev = c
	}
	return out
}

// HasLineBreaks reports whether word has anywhere to break at all, without segmenting it.
func HasLineBreaks(word string) bool {
	prev := rune(-1)
	for i, c := range word {
		if i > 0 && syllableStart(prev, c) {
			return true
		}
		prev = c
	}
	return false
}

// LineBreaks is the byte offsets inside word where a line breaks best: between words, as the
// dictionary segments it. A word with no Khmer and no zero-width space has none.
func LineBreaks(word string) []int {
	syl := SyllableBreaks(word)
	if len(syl) == 0 {
		return nil
	}
	return segment(word, syl)
}

//go:embed khmerdict/seafreq.txt
var seafreq []byte

// extra is words a home assistant says that seafreq.txt lacks (អង្សា, ភាគរយ…), in its format.
//
//go:embed khmerdict/extra.txt
var extra []byte

var (
	dictOnce sync.Once
	dict     map[string]float64 // word -> cost, -log of its share of the corpus
	unknown  float64            // cost of a syllable the dictionary has no word for
	maxSyl   = 1                // most syllables in one dictionary word
)

// DictPath is a word list of the owner's own that, when present, replaces the built-in one: one
// word per line, optionally a tab and its corpus count. A larger dictionary (say the 44k-word list
// khmer_segmenter builds on, whose terms keep it out of firmware) segments better; it is the owner's
// to put there.
var DictPath = filepath.Join(layout.StateDir, "khmer-words.txt")

// floor is the count a word is given when its list has none, or less: khmer_segmenter's cost model,
// which this follows — cost is -log10 of the word's share of the corpus, and a syllable no word
// covers costs five orders of magnitude more than the rarest word.
const floor = 5.0

func loadDict() {
	data := append(append([]byte{}, seafreq...), extra...)
	source := "built-in"
	if own, err := os.ReadFile(DictPath); err == nil && len(own) > 0 {
		data, source = append(own, extra...), DictPath
	}
	counts := map[string]float64{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		w, f, _ := strings.Cut(sc.Text(), "\t")
		w = strings.ReplaceAll(strings.TrimSpace(w), string(zwsp), "")
		if w == "" || strings.IndexFunc(w, func(c rune) bool { return !khmer(c) }) >= 0 {
			continue
		}
		n, err := strconv.ParseFloat(strings.TrimSpace(f), 64)
		if err != nil {
			n = 0
		}
		counts[w] += n
	}
	var total float64
	for _, n := range counts {
		total += max(n, floor)
	}
	dict = make(map[string]float64, len(counts))
	for w, n := range counts {
		dict[w] = -math.Log10(max(n, floor) / total)
		maxSyl = max(maxSyl, len(SyllableBreaks(w))+1)
	}
	unknown = -math.Log10(floor/total) + 5
	slog.Info("fonts: khmer word list loaded", "words", len(dict), "from", source)
}

// segment is the cheapest split of word into dictionary words and lone unknown syllables (Viterbi
// over the syllable boundaries syl), as the byte offsets between the pieces.
func segment(word string, syl []int) []int {
	dictOnce.Do(loadDict)
	pos := make([]int, 0, len(syl)+2)
	pos = append(append(append(pos, 0), syl...), len(word))
	n := len(pos) - 1
	best := make([]float64, n+1)
	from := make([]int, n+1)
	for j := 1; j <= n; j++ {
		best[j], from[j] = math.Inf(1), j-1
		for i := max(0, j-maxSyl); i < j; i++ {
			piece := word[pos[i]:pos[j]]
			// a sentence mark (។ ៕ …) rides on the word before it, which is what is looked up
			cost, ok := dict[strings.TrimRightFunc(piece, func(c rune) bool { return c >= '។' && c <= '៚' })]
			if !ok {
				if j-i > 1 {
					continue
				}
				cost = unknown
				if r, _ := utf8.DecodeRuneInString(piece); !khmer(r) {
					cost = 0 // digits, Latin: their own piece, free
				}
			}
			if c := best[i] + cost; c < best[j] {
				best[j], from[j] = c, i
			}
		}
	}
	var cuts []int
	for j := n; j > 0; j = from[j] {
		if from[j] > 0 {
			cuts = append(cuts, pos[from[j]])
		}
	}
	for l, r := 0, len(cuts)-1; l < r; l, r = l+1, r-1 {
		cuts[l], cuts[r] = cuts[r], cuts[l]
	}
	return cuts
}

// SplitToWidth cuts word at its LineBreaks into pieces each no wider than width as measure tells,
// filling every piece as far as it goes. A dictionary word too wide for a line by itself is cut
// between syllables; a syllable wider than the line is left whole (it overflows, as an unbreakable
// word does). Nil when word already fits or has nowhere to break.
func SplitToWidth(word string, width int, measure func(string) int) []string {
	return SplitToWidths(word, width, width, measure)
}

// SplitToWidths is SplitToWidth with the first piece held to first instead (the room left on a line
// already begun). When not even one syllable fits in first, the first piece comes out wider than
// first, which a caller can test for and start a new line instead.
func SplitToWidths(word string, first, rest int, measure func(string) int) []string {
	if measure(word) <= first {
		return nil
	}
	syl := SyllableBreaks(word)
	if len(syl) == 0 {
		return nil
	}
	words := append(segment(word, syl), len(word))
	syl = append(syl, len(word))
	var out []string
	start := 0
	for start < len(word) {
		width := rest
		if len(out) == 0 {
			width = first
		}
		cut := furthest(word, start, words, width, measure)
		if cut <= start {
			cut = furthest(word, start, syl, width, measure)
		}
		if cut <= start { // not one syllable fits: it overflows on a line of its own
			for _, b := range syl {
				if b > start {
					cut = b
					break
				}
			}
		}
		out = append(out, trimZW(word[start:cut]))
		start = cut
	}
	return out
}

// furthest is the last of breaks after start where word[start:b] still fits width, or start.
func furthest(word string, start int, breaks []int, width int, measure func(string) int) int {
	cut := start
	for _, b := range breaks {
		if b <= start {
			continue
		}
		if measure(word[start:b]) > width {
			break
		}
		cut = b
	}
	return cut
}

// trimZW drops zero-width spaces at the ends of a piece: they were only there to break at.
func trimZW(s string) string {
	return strings.TrimFunc(s, func(c rune) bool { return c == zwsp })
}
