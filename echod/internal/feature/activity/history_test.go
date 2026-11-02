package activity

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func endTurn(l *Log, heard string, how Ending, why Reason) {
	t := l.Begin(2, "Hey Jarvis", WakeWord)
	t.Listening()
	t.Heard(heard)
	t.Replying("a reply")
	if how == Canceled {
		t.Cancels(why)
	}
	t.Ends(how)
}

// The history keeps the last 10 turns, newest first, and drops the oldest.
func TestHistoryKeepsTenAndDropsTheOldest(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	ownSender(t, 64, func(component.Event) {})
	ClearHistory()
	t.Cleanup(ClearHistory)
	for i := 0; i < 12; i++ {
		endTurn(Get(), fmt.Sprintf("request %d", i), Answered, "")
	}
	got := History()
	if len(got) != 10 {
		t.Fatalf("%d turns kept", len(got))
	}
	if got[0].Heard != "request 11" || got[9].Heard != "request 2" {
		t.Errorf("newest %q, oldest %q", got[0].Heard, got[9].Heard)
	}
	if got[0].End != "completed" || got[0].Slot != 2 {
		t.Errorf("ended %q slot %d", got[0].End, got[0].Slot)
	}
}

// A canceled turn shows its reason, and clearing empties the history.
func TestHistoryShowsTheReasonAndClears(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	ownSender(t, 8, func(component.Event) {})
	ClearHistory()
	endTurn(Get(), "stop", Canceled, Interrupted)
	if got := History(); len(got) != 1 || got[0].End != "interrupted" {
		t.Fatalf("history %+v", got)
	}
	ClearHistory()
	if len(History()) != 0 {
		t.Error("clear left turns")
	}
}

// With Send voice text off the history keeps no words, as the event keeps none.
func TestHistoryWithholdsWordsWhileSendVoiceTextIsOff(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	ownSender(t, 8, func(component.Event) {})
	ClearHistory()
	t.Cleanup(ClearHistory)
	t.Cleanup(func() { Get().sendText(true) })
	Get().sendText(false)
	endTurn(Get(), "secret words", Answered, "")
	got := History()
	if len(got) != 1 || got[0].Heard != "" || got[0].Reply != "" || !got[0].Withheld {
		t.Errorf("history %+v", got)
	}
}
