package musicassistant

import (
	"reflect"
	"testing"
)

// The audiobooks read in order: by the author's surname, then the series in turn, each in its order,
// then the books outside a series by title.
func TestAudiobookOrderByAuthorThenSeries(t *testing.T) {
	book := func(name string, authors ...string) Entry { return Entry{Name: name, Authors: authors} }
	books := []Entry{
		book("Dust World (Unabridged)", "B. V. Larson"),
		book("03 First Strike", "Craig Alanson (Convergence)"),
		book("Five Years After", "William R. Forstchen"),
		book("01 Convergence", "Craig Alanson (Convergence)"),
		book("Artemis (Unabridged)", "Andy Weir"),
		book("Alliance: The Silver Ships, Book 14", "Ryk Brown"),
		book("The Silver Ships: The Silver Ships, Book 1 (Unabridged)", "Ryk Brown"),
		book("Head of the Dragon: Frontiers Saga Series, Book 6 (Unabridged)", "Ryk Brown"),
	}
	var got []string
	for _, b := range AudiobookOrder(books) {
		got = append(got, b.Name)
	}
	// Alanson, Brown, Forstchen, Larson, Weir: each author's books together, the series in turn (Frontiers
	// Saga before The Silver Ships), each in its book order.
	want := []string{
		"01 Convergence", "03 First Strike",
		"Head of the Dragon: Frontiers Saga Series, Book 6 (Unabridged)", "The Silver Ships: The Silver Ships, Book 1 (Unabridged)", "Alliance: The Silver Ships, Book 14",
		"Five Years After",
		"Dust World (Unabridged)",
		"Artemis (Unabridged)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %q, want %q", got, want)
	}
}

// The author's surname decides the order, whatever the first name: Boyett comes before Larson.
func TestAuthorKeyIsTheSurname(t *testing.T) {
	if authorKey(Entry{Authors: []string{"Mark Boyett"}}) >= authorKey(Entry{Authors: []string{"B. V. Larson"}}) {
		t.Errorf("Boyett did not sort before Larson by surname")
	}
	if authorKey(Entry{}) <= authorKey(Entry{Authors: []string{"Andy Weir"}}) {
		t.Errorf("a book with no author did not sort last")
	}
}

// The title order ignores the author, and the newest order puts the latest added first, with no date last.
func TestSortAudiobooksByTitleAndRecent(t *testing.T) {
	books := []Entry{
		{Name: "Zeta", DateAdded: "2026-01-02T00:00:00"},
		{Name: "alpha", DateAdded: "2026-03-05T00:00:00"},
		{Name: "Mid"},
	}
	var titles []string
	for _, b := range SortAudiobooks(books, AudiobookByTitle) {
		titles = append(titles, b.Name)
	}
	if !reflect.DeepEqual(titles, []string{"alpha", "Mid", "Zeta"}) {
		t.Errorf("by title = %q", titles)
	}
	var recent []string
	for _, b := range SortAudiobooks(books, AudiobookByRecent) {
		recent = append(recent, b.Name)
	}
	if !reflect.DeepEqual(recent, []string{"alpha", "Zeta", "Mid"}) {
		t.Errorf("newest = %q", recent)
	}
}
