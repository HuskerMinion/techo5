package lyrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	lrc := "[ar: A Band]\n[00:12.50] First line\n[00:05.00][01:02.25]Chorus\nno time here\n[00:20.123] Third\n[bad:time] x\n"
	got := Parse(lrc)
	want := []Line{
		{5 * time.Second, "Chorus"},
		{12500 * time.Millisecond, "First line"},
		{20123 * time.Millisecond, "Third"},
		{62250 * time.Millisecond, "Chorus"},
	}
	if len(got) != len(want) {
		t.Fatalf("Parse = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestAt(t *testing.T) {
	lines := []Line{{5 * time.Second, "one"}, {10 * time.Second, "two"}}
	for _, c := range []struct {
		pos       time.Duration
		cur, next string
		nextAt    time.Duration
	}{
		{0, "", "one", 5 * time.Second},
		{5 * time.Second, "one", "two", 10 * time.Second},
		{12 * time.Second, "two", "", 0},
	} {
		cur, next, at := At(lines, c.pos)
		if cur != c.cur || next != c.next || at != c.nextAt {
			t.Errorf("At(%v) = %q %q %v", c.pos, cur, next, at)
		}
	}
}

// With everything known the exact lookup answers; a song it does not have falls back to a search, where a
// version of another length, an instrumental and one without times are passed over.
func TestFind(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		if r.Header.Get("User-Agent") == "" {
			t.Error("no User-Agent")
		}
		switch r.URL.Path {
		case "/api/get":
			if r.URL.Query().Get("track_name") == "Known" {
				w.Write([]byte(`{"syncedLyrics":"[00:01.00] exact","duration":200}`))
				return
			}
			http.NotFound(w, r)
		case "/api/search":
			w.Write([]byte(`[{"duration":300,"syncedLyrics":"[00:01.00] live take"},
				{"duration":200,"instrumental":true,"syncedLyrics":""},
				{"duration":201,"syncedLyrics":""},
				{"duration":199,"syncedLyrics":"[00:02.00] studio"}]`))
		}
	}))
	defer srv.Close()
	old := Base
	Base = srv.URL
	defer func() { Base = old }()

	lines, err := Find(context.Background(), "Known", "A Band", "An Album", 200*time.Second)
	if err != nil || len(lines) != 1 || lines[0].Text != "exact" {
		t.Errorf("exact: %v %v", lines, err)
	}
	lines, err = Find(context.Background(), "Other", "A Band", "An Album", 200*time.Second)
	if err != nil || len(lines) != 1 || lines[0].Text != "studio" {
		t.Errorf("search: %v %v (asked %v)", lines, err, asked)
	}
	if _, err := Find(context.Background(), "", "A Band", "", 0); err != ErrNone {
		t.Errorf("no title: %v", err)
	}
}
