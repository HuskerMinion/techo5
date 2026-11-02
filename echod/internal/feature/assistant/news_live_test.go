//go:build live

package assistant

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// Reads the built-in news updates' real feeds: each should have an episode from the last few hours.
//
//	go test -tags live -run TestLiveNewsFeeds ./internal/feature/assistant/
func TestLiveNewsFeeds(t *testing.T) {
	for _, s := range builtInNews {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		ep, err := fetchEpisode(ctx, s.Feed)
		cancel()
		if err != nil {
			t.Errorf("%s: %v", s.Name, err)
			continue
		}
		t.Logf("%s: %q, %s ago, %s", s.Name, ep.Title, time.Since(ep.At).Round(time.Minute), ep.Audio)
		if time.Since(ep.At) > 12*time.Hour {
			t.Errorf("%s: its newest is from %s", s.Name, ep.At)
		}
	}
}

// Asks a real model for the news, the way a person would, with nothing played: the feeds are read for
// real. Needs a chat endpoint, as TestLiveAssistant does.
func TestLiveNewsAsked(t *testing.T) {
	base := os.Getenv("TECHO5_LLM")
	if base == "" {
		t.Skip("TECHO5_LLM is not set")
	}
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Brain().Set(config.Brain{Mode: config.BrainDirect, LLM: base, STT: "x:1", TTS: "x:1"}); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Brain().SetKey(keyFrom(t, os.Getenv("TECHO5_LLM_KEYFILE"))); err != nil {
		t.Fatal(err)
	}
	old := playEpisode
	t.Cleanup(func() { playEpisode = old })
	var played string
	playEpisode = func(_ string, title media.Title) error { played = title.Title; return nil }

	for _, c := range []struct{ said, want string }{
		{"play the fox news update", "Fox News Hourly Update"},
		{"play fox update today", "Fox News Hourly Update"},
		{"play NPR news", "NPR News Now"},
		{"what's the news", ""},
	} {
		played = ""
		Get().history = nil
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		reply, err := Get().Think(ctx, c.said)
		cancel()
		if err != nil {
			t.Fatalf("%q: %v", c.said, err)
		}
		t.Logf("%q -> %q, played %q", c.said, reply, played)
		if played != c.want {
			t.Errorf("%q: played %q, want %q", c.said, played, c.want)
		}
	}
}
