package logd

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLiveRingIsBounded(t *testing.T) {
	l := &liveLog{subs: map[*Follower]struct{}{}}
	for i := 0; i < liveLines*3; i++ {
		l.add(slog.LevelInfo, fmt.Sprintf("line %d", i))
	}
	got := l.tail(0)
	if len(got) != liveLines {
		t.Fatalf("kept %d lines, want %d", len(got), liveLines)
	}
	if got[len(got)-1].Text != fmt.Sprintf("line %d", liveLines*3-1) || got[0].Seq != uint64(liveLines*2+1) {
		t.Errorf("ring kept the wrong lines: first seq %d, last %q", got[0].Seq, got[len(got)-1].Text)
	}
	if n := len(l.tail(10)); n != 10 {
		t.Errorf("tail(10) gave %d", n)
	}

	// By bytes too: long lines push the oldest out before the line count does.
	b := &liveLog{subs: map[*Follower]struct{}{}}
	for i := 0; i < 100; i++ {
		b.add(slog.LevelInfo, strings.Repeat("x", liveLineMax))
	}
	if b.bytes > liveBytes+liveLineMax*2 {
		t.Errorf("kept %d bytes, want about %d", b.bytes, liveBytes)
	}
}

func TestLiveLineIsCut(t *testing.T) {
	l := &liveLog{subs: map[*Follower]struct{}{}}
	l.add(slog.LevelError, strings.Repeat("y", liveLineMax*4))
	if got := l.tail(1)[0].Text; len(got) > liveLineMax+8 {
		t.Errorf("a line of %d bytes was kept whole", len(got))
	}
}

// A follower that never reads must not make logging wait, and loses its oldest lines, not its newest.
func TestSlowFollowerDoesNotBlockLogging(t *testing.T) {
	l := &liveLog{subs: map[*Follower]struct{}{}}
	f := &Follower{C: make(chan Entry, followDepth), log: l}
	l.subs[f] = struct{}{}

	done := make(chan struct{})
	go func() {
		for i := 0; i < 5000; i++ {
			l.add(slog.LevelInfo, "x")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("logging waited for a follower that does not read")
	}
	if f.Lost() == 0 {
		t.Error("nothing reported lost")
	}
	var last Entry
	for len(f.C) > 0 {
		last = <-f.C
	}
	if last.Seq != 5000 {
		t.Errorf("newest line seen was %d, want 5000", last.Seq)
	}
	f.Close()
	if len(l.subs) != 0 {
		t.Error("closed follower still subscribed")
	}
}

func TestLiveBacklogAndFollowMeet(t *testing.T) {
	h := NewHandler("t", io.Discard)
	log := slog.New(h)
	log.Info("before", "k", "v")
	back, f := Live(0)
	defer f.Close()
	log.Warn("after")
	if len(back) == 0 || !strings.Contains(back[len(back)-1].Text, "before k=v") {
		t.Fatalf("backlog missing the line: %v", back)
	}
	select {
	case e := <-f.C:
		if e.Level != slog.LevelWarn || !strings.Contains(e.Text, "after") || e.Seq != back[len(back)-1].Seq+1 {
			t.Errorf("followed %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no line followed")
	}
}
