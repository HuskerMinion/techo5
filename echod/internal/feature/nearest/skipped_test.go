package nearest

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
)

// logged captures the log for the rest of the test.
func logged(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	was := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(was) })
	return &buf
}

// A wake word with nobody to ask says so, once while it stays that way, and again once the others
// have come back and gone again (#751): a device that had forgotten the house answered over the one
// somebody spoke to without a word in its log.
func TestNobodyToAskSaysSoOncePerChange(t *testing.T) {
	h := newHouse(t)
	buf := logged(t)
	alone := true
	h.n.peers = func() []announce.Peer {
		if alone {
			return nil
		}
		return []announce.Peer{{Name: "Kitchen", Address: "192.0.2.1", Port: 8181}}
	}
	heard := h.now.Add(-7 * time.Minute)
	h.n.peersHeard = func() time.Time { return heard }
	const line = "nearest device: answering at once, no other devices known"

	h.n.Woke(0)
	h.n.Woke(0)
	if got := strings.Count(buf.String(), line); got != 1 {
		t.Fatalf("two wakes alone logged %d lines, want 1:\n%s", got, buf)
	}
	if !strings.Contains(buf.String(), "devices=0") || !strings.Contains(buf.String(), "last_heard_ago=7m0s") {
		t.Errorf("the line says neither how many devices nor since when:\n%s", buf)
	}

	alone = false
	h.n.Woke(0)
	h.wait(300 * time.Millisecond)
	if !strings.Contains(buf.String(), "peers=1") {
		t.Errorf("the answering line has no peer count:\n%s", buf)
	}

	alone = true
	h.n.Woke(0)
	if got := strings.Count(buf.String(), line); got != 2 {
		t.Errorf("alone again after a decision logged %d lines in all, want 2:\n%s", got, buf)
	}
	if h.at0 != 3 || len(h.opened) != 1 {
		t.Errorf("answered at once %d times and after a decision %d, want 3 and 1", h.at0, len(h.opened))
	}
}

// A device that has never heard another says so, rather than a time.
func TestNobodyEverHeardSaysNever(t *testing.T) {
	h := newHouse(t)
	buf := logged(t)
	h.n.peers = func() []announce.Peer { return nil }
	h.n.peersHeard = func() time.Time { return time.Time{} }
	h.n.Woke(0)
	if !strings.Contains(buf.String(), "last_heard_ago=never") {
		t.Errorf("want last_heard_ago=never:\n%s", buf)
	}
}

// Every other way past the decision says why, once per change, and only for wake words: the next song
// word (Answer) is acted on as quietly as it always was.
func TestTheOtherWaysPastTheDecisionSayWhy(t *testing.T) {
	h := newHouse(t)
	buf := logged(t)

	h.n.busy = func() bool { return true }
	h.n.Woke(0)
	h.n.Woke(0)
	h.n.Answer("next", func() bool { return true }, func() {})
	h.n.busy = func() bool { return false }
	h.n.canAnswer = func(int) bool { return false }
	h.n.Woke(0)
	h.n.canAnswer = func(int) bool { return true }
	h.n.enabled = false
	h.n.Woke(0)

	out := buf.String()
	for line, want := range map[string]int{
		"answering at once, a turn is running here":          1,
		"answering at once, this device could not answer it": 1,
		"answering at once, nearest device answers is off":   1,
	} {
		if got := strings.Count(out, line); got != want {
			t.Errorf("%q logged %d times, want %d:\n%s", line, got, want, out)
		}
	}
	if h.at0 != 4 {
		t.Errorf("answered at once %d times, want 4", h.at0)
	}
}
