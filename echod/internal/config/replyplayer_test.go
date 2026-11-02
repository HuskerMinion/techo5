package config

import (
	"path/filepath"
	"testing"
)

func TestReplyPlayerValidation(t *testing.T) {
	for in, want := range map[string]string{"": "", "  ": "", " Media_Player.Kitchen_Dot ": "media_player.kitchen_dot", "media_player.x1": "media_player.x1"} {
		if got, ok := ValidReplyPlayer(in); !ok || got != want {
			t.Errorf("%q: got %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"media_player.", "light.kitchen", "kitchen_dot", "media_player.a b", "media_player.a.b", "media_player.a;b"} {
		if _, ok := ValidReplyPlayer(in); ok {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestReplyPlayerIsSavedAndSurvivesBrainSet(t *testing.T) {
	Use(filepath.Join(t.TempDir(), "state.json"))
	if err := Set().Brain().SetReplyPlayer("light.x"); err == nil {
		t.Fatal("a light was accepted")
	}
	if err := Set().Brain().SetReplyPlayer("media_player.dot"); err != nil {
		t.Fatal(err)
	}
	if err := Set().Brain().Set(Brain{STT: "a:1"}); err != nil {
		t.Fatal(err)
	}
	if got := Get().Brain.ReplyPlayer; got != "media_player.dot" {
		t.Fatalf("the brain form cleared the player: %q", got)
	}
}
