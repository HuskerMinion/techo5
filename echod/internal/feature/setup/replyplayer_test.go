package setup

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The page keeps a media player, clears it when emptied, and refuses anything else.
func TestSavingWhereRepliesPlay(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if saveReplyPlayer("media_player.kitchen_dot") != "" || config.Get().Brain.ReplyPlayer != "media_player.kitchen_dot" {
		t.Fatal("a media player was not kept")
	}
	if saveReplyPlayer("light.kitchen") == "" || config.Get().Brain.ReplyPlayer != "media_player.kitchen_dot" {
		t.Fatal("a light replaced the player")
	}
	if saveReplyPlayer("  ") != "" || config.Get().Brain.ReplyPlayer != "" {
		t.Fatal("an empty box did not go back to this device")
	}
}
