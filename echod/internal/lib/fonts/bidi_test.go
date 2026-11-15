package fonts

import "testing"

func TestVisualOrder(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		// no right-to-left letters: unchanged
		{"", ""},
		{"Hello, world 12:30", "Hello, world 12:30"},
		{"(Kitchen) 21°C", "(Kitchen) 21°C"},
		// Hebrew alone
		{"שלום", "םולש"},
		{"שלום עולם", "םלוע םולש"},
		{"שלום!", "!םולש"},
		// Hebrew with numbers: numbers keep their own order and stay where they belong
		{"12:30 שלום", "םולש 12:30"},
		{"שלום 12:30", "12:30 םולש"},
		{"10% סוללה", "הללוס 10%"},
		{"סוללה 10%", "10% הללוס"},
		{"Kitchen: מטבח 3", "Kitchen: 3 חבטמ"},
		{"חדר 2 קומה 3", "3 המוק 2 רדח"},
		// English inside Hebrew, Hebrew inside English
		{"Room מטבח", "Room חבטמ"},
		{"מצב Echo פעיל", "ליעפ Echo בצמ"},
		// brackets are mirrored in right-to-left text, not in left-to-right
		{"מטבח (Echo)", "(Echo) חבטמ"},
		{"(מטבח)", "(חבטמ)"},
		// nikud stays on its letter
		{"שָׁלוֹם", "םוֹלשָׁ"},
		// each line on its own
		{"Kitchen\nמטבח", "Kitchen\nחבטמ"},
	} {
		if got := VisualOrder(c.in); got != c.want {
			t.Errorf("VisualOrder(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestVisualOrderNotesHebrew(t *testing.T) {
	sawHebrew.Store(false)
	t.Cleanup(func() { sawHebrew.Store(false) })
	VisualOrder("Kitchen 12:30")
	if SawHebrew() {
		t.Fatal("Hebrew noted for English")
	}
	VisualOrder("مرحبا")
	if SawHebrew() {
		t.Fatal("Hebrew noted for Arabic")
	}
	VisualOrder("מטבח")
	if !SawHebrew() {
		t.Fatal("Hebrew not noted")
	}
}
