package setup

import (
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// countReconnects stands in for the device's reconnect for the length of a test, and counts them.
func countReconnects(t *testing.T) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	was := whenIdle
	whenIdle = &idleReconnect{busy: func() bool { return false }, emit: func() { n.Add(1) }, look: time.Millisecond}
	t.Cleanup(func() { whenIdle = was })
	return &n
}

// The page keeps a media player, clears it when emptied, and refuses anything else.
func TestSavingWhereRepliesPlay(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	countReconnects(t)
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

// Choosing a player where there was none, or clearing it, has Home Assistant reconnect to read what the
// device now says it is (#715); one player for another, or a save that is refused, does not.
func TestWhereRepliesPlayReconnectsOnlyWhenItMatters(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	reconnects := countReconnects(t)
	// Long enough that a reconnect asked for is still waiting when the test looks.
	whenIdle.look = 50 * time.Millisecond
	// settled waits for the reconnects to come to want, and says how many there are.
	settled := func(want int32) int32 {
		for range 1000 {
			if reconnects.Load() >= want {
				break
			}
			time.Sleep(time.Millisecond)
		}
		return reconnects.Load()
	}
	asked := func() bool {
		whenIdle.mu.Lock()
		defer whenIdle.mu.Unlock()
		return whenIdle.pending
	}
	for _, step := range []struct {
		player string
		want   int32
	}{
		{"media_player.kitchen", 1}, // chosen
		{"media_player.office", 1},  // another player
		{"light.kitchen", 1},        // refused
		{"", 2},                     // cleared
		{" ", 2},                    // still cleared
	} {
		before := reconnects.Load()
		saveReplyPlayer(step.player)
		if asked() != (step.want > before) {
			t.Fatalf("after %q: a reconnect asked for is %v", step.player, asked())
		}
		if got := settled(step.want); got != step.want {
			t.Fatalf("after %q: %d reconnects, want %d", step.player, got, step.want)
		}
	}
}
