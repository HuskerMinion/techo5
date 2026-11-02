package activity

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// With Send voice text off, the words stay on the device: not in the sensors, and not in either event,
// while the rest of the turn still goes.
func TestSendVoiceTextOffKeepsTheWords(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	l := Get()
	l.Heard("turn on the porch light")
	l.Replied("Turned on the porch light.")

	l.sendText(false)
	if !config.Get().Security.WordsKept {
		t.Fatal("the choice was not saved")
	}
	if l.heard.Get() != "" || l.reply.Get() != "" {
		t.Errorf("Last heard %q and Last reply %q, want both emptied", l.heard.Get(), l.reply.Get())
	}
	l.Heard("what time is it")
	l.Replied("It's noon.")
	if l.heard.Get() != "" || l.reply.Get() != "" {
		t.Errorf("Last heard %q and Last reply %q, want both empty", l.heard.Get(), l.reply.Get())
	}

	for _, name := range []string{TurnEvent, LegacyTurnEvent} {
		got := caught(t, name)
		turn := l.Begin(1, "okay nabu", WakeWord)
		turn.Listening()
		turn.Heard("what time is it")
		turn.Replying("It's noon.")
		turn.Ends(Answered)
		data := one(t, got)
		if data["heard"] != "" || data["reply"] != "" {
			t.Errorf("%s carries heard %q and reply %q", name, data["heard"], data["reply"])
		}
		if data["id"] == "" || data["wake_word"] == "" {
			t.Errorf("%s lost the rest of the turn: %v", name, data)
		}
	}

	l.sendText(true)
	l.Heard("what time is it")
	if l.heard.Get() != "what time is it" {
		t.Errorf("Last heard %q after turning it back on", l.heard.Get())
	}
}

// The daemon's log leaves the words out too while Send voice text is off: it goes into a diagnostics
// bundle that may be posted.
func TestSaidIsLeftOutOfTheLogWhileOff(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if got := Said("what time is it"); got != "what time is it" {
		t.Errorf("Said is %q with Send voice text on", got)
	}
	Get().sendText(false)
	if got := Said("what time is it"); got == "what time is it" || got == "" {
		t.Errorf("Said is %q with Send voice text off", got)
	}
	if got := Said(""); got != "" {
		t.Errorf("Said is %q for nothing said", got)
	}
}
