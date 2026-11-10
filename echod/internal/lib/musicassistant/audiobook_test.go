package musicassistant

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// An audiobook is read in full by its id and provider: its chapters come with it, in order, with
// where each starts and ends. A podcast's episodes are read by the podcast's id and provider.
func TestAudiobookAndPodcastCalls(t *testing.T) {
	type call struct {
		Command string         `json:"command"`
		Args    map[string]any `json:"args"`
	}
	var got []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var c call
		if err := json.Unmarshal(body, &c); err != nil {
			t.Errorf("request is not a command: %v", err)
		}
		got = append(got, c)
		switch c.Command {
		case "music/audiobooks/get":
			io.WriteString(w, `{"item_id":"41","provider":"library","name":"01 Convergence","media_type":"audiobook",
				"uri":"library://audiobook/41","duration":61017,
				"metadata":{"chapters":[{"position":0,"name":"Chapter 1","start":0.0,"end":15.2},
				            {"position":1,"name":"Chapter 2","start":15.2,"end":1376.4}]}}`)
		case "music/podcasts/podcast_episodes":
			io.WriteString(w, `[{"item_id":"ep1","provider":"ytmusic","name":"Episode 7","media_type":"podcast_episode"}]`)
		default:
			t.Errorf("unexpected command %q", c.Command)
		}
	}))
	defer srv.Close()
	c := Client{URL: srv.URL}

	book, chapters, err := c.Audiobook(context.Background(), Entry{ItemID: "41", Provider: "library"})
	if err != nil {
		t.Fatalf("Audiobook: %v", err)
	}
	if book.Name != "01 Convergence" || book.Duration != 61017 {
		t.Errorf("entry = %q, %d seconds; want the named book of 61017 seconds", book.Name, book.Duration)
	}
	want := []Chapter{{0, "Chapter 1", 0, 15.2}, {1, "Chapter 2", 15.2, 1376.4}}
	if !reflect.DeepEqual(chapters, want) {
		t.Errorf("chapters = %+v, want %+v", chapters, want)
	}

	episodes, err := c.PodcastEpisodes(context.Background(), Entry{ItemID: "pod1", Provider: "library"})
	if err != nil {
		t.Fatalf("PodcastEpisodes: %v", err)
	}
	if len(episodes) != 1 || episodes[0].Name != "Episode 7" {
		t.Errorf("episodes = %+v, want the one episode", episodes)
	}

	wantCalls := []call{
		{"music/audiobooks/get", map[string]any{"item_id": "41", "provider_instance_id_or_domain": "library"}},
		{"music/podcasts/podcast_episodes", map[string]any{"item_id": "pod1", "provider_instance_id_or_domain": "library"}},
	}
	if !reflect.DeepEqual(got, wantCalls) {
		t.Errorf("calls = %+v, want %+v", got, wantCalls)
	}
}

// The kinds are the names the server has for the lists: podcasts and audiobooks as music kinds.
func TestPodcastAndAudiobookKinds(t *testing.T) {
	if KindPodcasts != "podcasts" || KindAudiobooks != "audiobooks" {
		t.Errorf("kinds = %q, %q; want podcasts, audiobooks", KindPodcasts, KindAudiobooks)
	}
}

// A chapter is an entry of its book: numbered from one, its length from its start and end, and it
// plays from the book's own address and starts where the chapter does.
func TestChapterEntryNumbersAndPlaysFromTheBook(t *testing.T) {
	book := Entry{ItemID: "41", Provider: "library", URI: "library://audiobook/41"}
	got := ChapterEntry(book, Chapter{Position: 2, Name: "Chapter 3", Start: 1376.4, End: 3016.1})
	if got.Name != "Chapter 3" || got.TrackNumber != 3 || got.Duration != 1639 || got.Start != 1376.4 {
		t.Errorf("chapter = %+v; want chapter 3, 1639 seconds, starting at 1376.4", got)
	}
	if got.URI != book.URI || got.MediaType != "chapter" {
		t.Errorf("chapter plays %q as %q; want the book's address as a chapter", got.URI, got.MediaType)
	}
}
