package musicassistant

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

type listenCall struct {
	Command string         `json:"command"`
	Args    map[string]any `json:"args"`
}

// listenServer answers each command with what answers holds for it, and keeps the calls.
func listenServer(t *testing.T, answers map[string]string) (Client, *[]listenCall) {
	t.Helper()
	var got []listenCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var c listenCall
		if err := json.Unmarshal(body, &c); err != nil {
			t.Errorf("request is not a command: %v", err)
		}
		got = append(got, c)
		a, ok := answers[c.Command]
		if !ok {
			t.Errorf("unexpected command %q", c.Command)
			a = "null"
		}
		io.WriteString(w, a)
	}))
	t.Cleanup(srv.Close)
	return Client{URL: srv.URL}, &got
}

// The audiobooks folded by series ask for the collections collapsed; a series comes as a collection
// with its parts in order, and takes its authors, picture and newest date from them. A book on its own
// stays a book.
func TestAudiobooksBySeries(t *testing.T) {
	c, got := listenServer(t, map[string]string{"music/audiobooks/library_items": `[
		{"item_id":"c1","provider":"library","name":"Die drei ???","media_type":"collection","uri":"library://collection/c1",
		 "items":[{"item_id":"1","provider":"library","name":"Folge 1","media_type":"audiobook","uri":"library://audiobook/1",
		           "authors":[{"name":"Robert Arthur","item_id":"9"}],"date_added":"2025-01-01T00:00:00",
		           "metadata":{"images":[{"type":"thumb","path":"x","provider":"library","proxy_id":"p1"}]}},
		          {"item_id":"2","provider":"library","name":"Folge 2","media_type":"audiobook","uri":"library://audiobook/2",
		           "date_added":"2026-02-01T00:00:00","resume_position_ms":61000}]},
		{"item_id":"7","provider":"library","name":"Alone","media_type":"audiobook","uri":"library://audiobook/7","authors":["A. Writer"]}]`})
	books, err := c.AudiobooksBySeries(context.Background(), 40, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 2 {
		t.Fatalf("got %d entries, want a series and a book: %+v", len(books), books)
	}
	s := books[0]
	if s.MediaType != "collection" || len(s.Items) != 2 || s.Items[0].Name != "Folge 1" || s.Items[1].ResumeMS != 61000 {
		t.Errorf("series = %+v", s)
	}
	if !reflect.DeepEqual(s.Authors, []string{"Robert Arthur"}) || s.Image == nil || s.Image.ProxyID != "p1" || s.DateAdded != "2026-02-01T00:00:00" {
		t.Errorf("series authors %v, picture %+v, added %q; want its parts'", s.Authors, s.Image, s.DateAdded)
	}
	if books[1].MediaType != "audiobook" || !reflect.DeepEqual(books[1].Authors, []string{"A. Writer"}) {
		t.Errorf("book = %+v", books[1])
	}
	want := []listenCall{{"music/audiobooks/library_items", map[string]any{"limit": 20.0, "offset": 40.0, "order_by": "sort_name", "collapse_collections": true}}}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("calls = %+v\nwant  %+v", *got, want)
	}
}

// What is in progress is asked for this token's user only; an episode is read in full by its id and
// provider, with where it was left and its description.
func TestInProgressAndAnEpisode(t *testing.T) {
	c, got := listenServer(t, map[string]string{
		"music/in_progress_items": `[{"item_id":"5","provider":"library","name":"Book","media_type":"audiobook","uri":"library://audiobook/5"},
			{"item_id":"e1","provider":"rss--a","name":"Ep","media_type":"podcast_episode","uri":"rss--a://podcast_episode/e1"}]`,
		"music/podcasts/podcast_episode": `{"item_id":"e1","provider":"rss--a","name":"Ep","media_type":"podcast_episode",
			"duration":1800,"resume_position_ms":900000,"fully_played":false,
			"metadata":{"description":"<p>Part one &amp; two.</p><p>More<br>lines</p>","release_date":"2024-05-06T00:00:00"}}`,
	})
	ctx := context.Background()
	items, err := c.InProgress(ctx, 12)
	if err != nil || len(items) != 2 || items[1].MediaType != "podcast_episode" {
		t.Fatalf("in progress = %+v, %v", items, err)
	}
	ep, err := c.PodcastEpisode(ctx, items[1])
	if err != nil {
		t.Fatal(err)
	}
	if ep.ResumeMS != 900000 || ep.Duration != 1800 || ep.FullyPlayed || ep.Year != 2024 {
		t.Errorf("episode = %+v", ep)
	}
	if ep.Description != "Part one & two.\nMore\nlines" {
		t.Errorf("description = %q", ep.Description)
	}
	want := []listenCall{
		{"music/in_progress_items", map[string]any{"limit": 12.0, "all_users": false}},
		{"music/podcasts/podcast_episode", map[string]any{"item_id": "e1", "provider_instance_id_or_domain": "rss--a"}},
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("calls = %+v\nwant  %+v", *got, want)
	}
}

// The queue's spoken-word controls: a play from a chapter or from the start, a skip, a speed, and what
// it plays now.
func TestTheQueuesListeningCalls(t *testing.T) {
	c, got := listenServer(t, map[string]string{
		"player_queues/play_media":         `null`,
		"player_queues/skip":               `null`,
		"player_queues/set_playback_speed": `null`,
		"player_queues/get": `{"queue_id":"q1","active":true,"state":"playing","playback_speed":1.25,
			"current_item":{"queue_item_id":"x","name":"Book","media_item":{"item_id":"5","provider":"library","media_type":"audiobook","uri":"library://audiobook/5","name":"Book"}}}`,
	})
	ctx := context.Background()
	for _, err := range []error{
		c.PlayFrom(ctx, "q1", "library://audiobook/5", 0, false),
		c.PlayFrom(ctx, "q1", "rss--a://podcast_episode/e1", -1, true),
		c.PlayFrom(ctx, "q1", "library://audiobook/5", -1, false),
		c.Skip(ctx, "q1", -30),
		c.SetSpeed(ctx, "q1", 1.5),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	now, err := c.Now(ctx, "q1")
	if err != nil {
		t.Fatal(err)
	}
	if !now.Active || now.State != "playing" || now.Speed != 1.25 || now.URI != "library://audiobook/5" || !now.SpokenWord() ||
		now.Name != "Book" || now.ItemName != "Book" {
		t.Errorf("now = %+v", now)
	}
	want := []listenCall{
		{"player_queues/play_media", map[string]any{"queue_id": "q1", "media": "library://audiobook/5", "option": "replace", "start_item": "0"}},
		{"player_queues/play_media", map[string]any{"queue_id": "q1", "media": "rss--a://podcast_episode/e1", "option": "replace", "start_from_beginning": true}},
		{"player_queues/play_media", map[string]any{"queue_id": "q1", "media": "library://audiobook/5", "option": "replace"}},
		{"player_queues/skip", map[string]any{"queue_id": "q1", "seconds": -30.0}},
		{"player_queues/set_playback_speed", map[string]any{"queue_id": "q1", "speed": 1.5}},
		{"player_queues/get", map[string]any{"queue_id": "q1"}},
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("calls = %+v\nwant  %+v", *got, want)
	}
	if (QueueNow{MediaType: "track"}).SpokenWord() {
		t.Error("a track was taken for spoken word")
	}
}

// An audiobook's details are read as the server sends them: narrators as names or artists, the
// publisher, and the place this user left it.
func TestAnAudiobooksDetails(t *testing.T) {
	var e Entry
	if err := json.Unmarshal([]byte(`{"item_id":"3","provider":"library","name":"B","media_type":"audiobook","uri":"library://audiobook/3",
		"authors":["Ann Author",{"name":"Second Author"}],"narrators":[{"item_id":"8","name":"Ned Reader"}],"publisher":"House",
		"duration":36000,"resume_position_ms":null,"fully_played":true,"metadata":{"description":"Plain words."}}`), &e); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e.Authors, []string{"Ann Author", "Second Author"}) || !reflect.DeepEqual(e.Narrators, []string{"Ned Reader"}) {
		t.Errorf("authors %v, narrators %v", e.Authors, e.Narrators)
	}
	if e.Publisher != "House" || e.Description != "Plain words." || !e.FullyPlayed || e.ResumeMS != 0 {
		t.Errorf("entry = %+v", e)
	}
}

// A description is kept to a few kilobytes, cut at a character's start.
func TestALongDescriptionIsCut(t *testing.T) {
	long := strings.Repeat("é", maxDescription)
	var e Entry
	if err := json.Unmarshal([]byte(`{"name":"x","metadata":{"description":"`+long+`"}}`), &e); err != nil {
		t.Fatal(err)
	}
	if len(e.Description) > maxDescription+len("…") || !utf8.ValidString(e.Description) || !strings.HasSuffix(e.Description, "…") {
		t.Errorf("description of %d bytes, valid %v", len(e.Description), utf8.ValidString(e.Description))
	}
}

func TestPlainText(t *testing.T) {
	for in, want := range map[string]string{
		"Just words.":                          "Just words.",
		"<b>Bold</b> and&nbsp;more":            "Bold and more",
		"<div>One</div>\n\n<div>Two</div>":     "One\nTwo",
		"Line one<br/>Line  two &quot;x&quot;": "Line one\nLine two \"x\"",
		"<style>p { color: red }</style><p>Shown</p><script>var a = 1 < 2;</script>": "Shown",
		"<!-- a note --><b>a < b and c > d</b>":                                      "a < b and c > d",
		"if x <3 and y>2 &amp; done":                                                 "if x <3 and y>2 & done",
	} {
		if got := PlainText(in); got != want {
			t.Errorf("PlainText(%q) = %q, want %q", in, got, want)
		}
	}
}
