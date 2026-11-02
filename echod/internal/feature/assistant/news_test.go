package assistant

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// newsFeed is a feed as the news podcasts send it: newest first, a page and a non-audio item about.
const newsFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd">
<channel><title>Example News Update</title>
<item><title>A page, not an episode</title><pubDate>Mon, 02 Nov 2026 10:00:00 -0000</pubDate>
 <enclosure url="https://cdn.example.com/a.pdf" type="application/pdf"/></item>
<item><title>5AM ET Newscast</title><pubDate>Mon, 02 Nov 2026 09:06:00 -0000</pubDate>
 <enclosure url="https://cdn.example.com/5am.mp3" length="0" type="audio/mpeg"/></item>
<item><title>4AM ET Newscast</title><pubDate>Mon, 02 Nov 2026 08:06:00 -0000</pubDate>
 <enclosure url="https://cdn.example.com/4am.mp3" length="0" type="audio/mpeg"/></item>
</channel></rss>`

func TestTheNewestEpisodeWithAudioIsPlayed(t *testing.T) {
	ep, err := newestEpisode(strings.NewReader(newsFeed))
	if err != nil {
		t.Fatal(err)
	}
	if ep.Audio != "https://cdn.example.com/5am.mp3" || ep.Title != "5AM ET Newscast" {
		t.Fatalf("got %+v, want the 5 AM newscast", ep)
	}
}

func TestANewerEpisodeFurtherDownIsTheOnePlayed(t *testing.T) {
	feed := strings.Replace(newsFeed, "08:06:00", "11:06:00", 1)
	ep, err := newestEpisode(strings.NewReader(feed))
	if err != nil {
		t.Fatal(err)
	}
	if ep.Audio != "https://cdn.example.com/4am.mp3" {
		t.Fatalf("got %s, want the one published last", ep.Audio)
	}
}

func TestAFeedWithNoAudioSaysSo(t *testing.T) {
	for _, feed := range []string{
		`<rss><channel><item><title>x</title><enclosure url="file:///etc/passwd" type="audio/mpeg"/></item></channel></rss>`,
		`<rss><channel><title>nothing</title></channel></rss>`,
		`not a feed at all`,
	} {
		if _, err := newestEpisode(strings.NewReader(feed)); err == nil {
			t.Errorf("%q: no error", feed)
		}
	}
}

func TestTheSourceAskedForIsPicked(t *testing.T) {
	all := builtInNews
	for asked, want := range map[string]string{
		"Fox":                    "Fox News Hourly Update",
		"fox news":               "Fox News Hourly Update",
		"the Fox news update":    "Fox News Hourly Update",
		"NPR":                    "NPR News Now",
		"ABC news":               "ABC News Update",
		"Fox News Hourly Update": "Fox News Hourly Update",
	} {
		got, err := pickNews(asked, all, 0)
		if err != nil || got.Name != want {
			t.Errorf("%q: got %q, %v; want %q", asked, got.Name, err, want)
		}
	}
}

func TestNoSourceNamedAsksWhichUnlessTheHouseholdHasOne(t *testing.T) {
	for _, asked := range []string{"", "the news", "today's news update", "my news briefing"} {
		_, err := pickNews(asked, builtInNews, 0)
		if err == nil || !strings.Contains(err.Error(), "ask which") {
			t.Errorf("%q: got %v, want to be told to ask which", asked, err)
		}
	}
	own := append([]config.NewsSource{{Name: "City Radio", Feed: "https://example.com/news.xml"}}, builtInNews...)
	got, err := pickNews("the news", own, 1)
	if err != nil || got.Name != "City Radio" {
		t.Fatalf("got %q, %v; want the household's first", got.Name, err)
	}
}

func TestASourceNotThereIsNotSwappedForAnother(t *testing.T) {
	_, err := pickNews("BBC", builtInNews, 0)
	if err == nil || !strings.Contains(err.Error(), "NPR News Now") {
		t.Fatalf("got %v, want an error listing the sources", err)
	}
}

func TestTheHouseholdsSourcesComeFirstAndReplaceABuiltInOfTheSameName(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	b := config.Get().Brain
	b.News = "City Radio https://example.com/news.xml\nNPR News Now https://example.com/npr.xml"
	if err := config.Set().Brain().Set(b); err != nil {
		t.Fatal(err)
	}
	all, own := newsSources()
	if own != 2 || all[0].Name != "City Radio" || all[1].Feed != "https://example.com/npr.xml" {
		t.Fatalf("got %+v (%d own)", all, own)
	}
	n := 0
	for _, s := range all {
		if s.Name == "NPR News Now" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("NPR News Now is listed %d times", n)
	}
}

func TestNewsPlaysOnceAsANamedTrack(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	oldFetch, oldPlay := fetchEpisode, playEpisode
	t.Cleanup(func() { fetchEpisode, playEpisode = oldFetch, oldPlay })
	fetchEpisode = func(_ context.Context, feed string) (episode, error) {
		if feed != "https://feeds.megaphone.fm/FOXM2252206613" {
			t.Errorf("fetched %s", feed)
		}
		return episode{Title: "5AM ET Newscast", Audio: "https://cdn.example.com/5am.mp3", At: time.Now().Add(-time.Hour)}, nil
	}
	var played string
	var title media.Title
	playEpisode = func(audio string, t media.Title) error { played, title = audio, t; return nil }

	got, err := playNews("fox")
	if err != nil || got != "playing Fox News Hourly Update" {
		t.Fatalf("got %q, %v", got, err)
	}
	if played != "https://cdn.example.com/5am.mp3" || title.Title != "Fox News Hourly Update" || title.Subtitle != "5AM ET Newscast" {
		t.Fatalf("played %s as %+v", played, title)
	}
}

func TestAnOldNewsUpdateIsSaidToBeOld(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	oldFetch, oldPlay := fetchEpisode, playEpisode
	t.Cleanup(func() { fetchEpisode, playEpisode = oldFetch, oldPlay })
	fetchEpisode = func(context.Context, string) (episode, error) {
		return episode{Audio: "https://cdn.example.com/old.mp3", At: time.Now().AddDate(0, 0, -3)}, nil
	}
	playEpisode = func(string, media.Title) error { return nil }
	got, err := playNews("NPR")
	if err != nil || !strings.Contains(got, "its newest is from") {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestNewsThatDoesNotPlaySaysSo(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	oldFetch, oldPlay := fetchEpisode, playEpisode
	t.Cleanup(func() { fetchEpisode, playEpisode = oldFetch, oldPlay })
	fetchEpisode = func(context.Context, string) (episode, error) {
		return episode{Audio: "https://cdn.example.com/5am.mp3"}, nil
	}
	playEpisode = func(string, media.Title) error { return errors.New("nothing arrived from the stream in 10s") }
	if got, err := playNews("ABC"); err == nil || !strings.Contains(err.Error(), "ABC News Update did not play") {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestPlayNewsIsRefusedAfterReadingTheWeb(t *testing.T) {
	if allowedAfterWeb["play_news"] || allowedAfterStations["play_news"] {
		t.Fatal("play_news changes what plays, so a turn that has read the web must not run it")
	}
}

func TestAFeedInAnOlderCharacterSetIsRead(t *testing.T) {
	feed := strings.Replace(newsFeed, `encoding="UTF-8"`, `encoding="ISO-8859-1"`, 1)
	ep, err := newestEpisode(strings.NewReader(feed))
	if err != nil || ep.Audio != "https://cdn.example.com/5am.mp3" {
		t.Fatalf("got %+v, %v", ep, err)
	}
}
