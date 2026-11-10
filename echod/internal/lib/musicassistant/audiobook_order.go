package musicassistant

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The audiobooks' order on a page: by author's surname, then by series, then by book in the series, then
// by title. Music Assistant's own orders do not give an author's surname order, and it keeps no series
// for these books, so both are read from what the titles and the author's name say.

var (
	leadingNumber = regexp.MustCompile(`^\s*(\d+)\s+`)
	bookNumber    = regexp.MustCompile(`(?i)^(.*?),?\s*(?:series\s*,?\s*)?book\s+(\d+)\b`)
	parenthesized = regexp.MustCompile(`\(([^)]*)\)`)
)

// AudiobookOrder is the audiobooks in order: each author's books together, by surname, and within an
// author the series in turn, each in its own book order; the books that are not in a series come after
// the series, by title. The list is not changed.
func AudiobookOrder(books []Entry) []Entry {
	type keyed struct {
		e              Entry
		author, series string
		seq            int
		title          string
	}
	ks := make([]keyed, len(books))
	for i, b := range books {
		k := keyed{e: b, title: strings.ToLower(b.Name)}
		k.author = authorKey(b)
		k.series, k.seq = seriesOf(b)
		ks[i] = k
	}
	sort.SliceStable(ks, func(i, j int) bool {
		a, b := ks[i], ks[j]
		if a.author != b.author {
			return a.author < b.author
		}
		if (a.series == "") != (b.series == "") {
			return a.series != "" // series first, then books on their own
		}
		if a.series != b.series {
			return a.series < b.series
		}
		if (a.seq == 0) != (b.seq == 0) {
			return a.seq != 0 // numbered books first, in order
		}
		if a.seq != b.seq {
			return a.seq < b.seq
		}
		return a.title < b.title
	})
	out := make([]Entry, len(ks))
	for i, k := range ks {
		out[i] = k.e
	}
	return out
}

// The orders an audiobooks page can be put in.
const (
	AudiobookByAuthor = iota // the author, then series and book (AudiobookOrder)
	AudiobookByTitle         // by title
	AudiobookByRecent        // newest added first
	AudiobookOrders          // how many orders there are
)

// SortAudiobooks puts books in the order mode names. The list is not changed.
func SortAudiobooks(books []Entry, mode int) []Entry {
	switch mode {
	case AudiobookByTitle:
		out := append([]Entry(nil), books...)
		sort.SliceStable(out, func(i, j int) bool {
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		})
		return out
	case AudiobookByRecent:
		out := append([]Entry(nil), books...)
		// ISO times sort as text; a book with no date goes last.
		sort.SliceStable(out, func(i, j int) bool {
			a, b := out[i].DateAdded, out[j].DateAdded
			if (a == "") != (b == "") {
				return a != ""
			}
			return a > b
		})
		return out
	}
	return AudiobookOrder(books)
}

// authorKey sorts by the author's surname, then their name: "B. V. Larson" as "larson b. v.". A book with
// no author sorts after the others.
func authorKey(b Entry) string {
	if len(b.Authors) == 0 {
		return "\xff"
	}
	name := strings.TrimSpace(parenthesized.ReplaceAllString(b.Authors[0], ""))
	words := strings.Fields(strings.ToLower(name))
	if len(words) == 0 {
		return "\xff"
	}
	surname := words[len(words)-1]
	return surname + "\x00" + strings.Join(words, " ")
}

// seriesOf is a book's series and its place in it (0 when it has none or no number). The author's name
// may name the series in brackets, as "Craig Alanson (Convergence)"; a number in front of the title is
// the place; a title with ", Book N" names both.
func seriesOf(b Entry) (string, int) {
	seq := 0
	name := b.Name
	if m := leadingNumber.FindStringSubmatch(name); m != nil {
		seq, _ = strconv.Atoi(m[1])
		name = strings.TrimSpace(name[len(m[0]):])
	}
	if len(b.Authors) > 0 {
		if m := parenthesized.FindStringSubmatch(b.Authors[0]); m != nil {
			return strings.TrimSpace(m[1]), seq
		}
	}
	if m := bookNumber.FindStringSubmatch(b.Name); m != nil {
		s := strings.TrimSpace(m[1])
		if i := strings.LastIndex(s, " - "); i >= 0 {
			s = s[i+3:]
		}
		if i := strings.LastIndex(s, ":"); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), " Series"))
		n, _ := strconv.Atoi(m[2])
		if s != "" {
			return s, n
		}
	}
	if seq > 0 && name != "" {
		return name, seq
	}
	return "", 0
}
