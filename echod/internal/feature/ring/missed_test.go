package ring

import (
	"testing"
	"time"
)

func TestAMissedRingIsSaidThenForgotten(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	was := missed.now
	missed.now = func() time.Time { return now }
	t.Cleanup(func() { missed.now = was; ClearMissed() })

	if Missing() != nil {
		t.Fatal("something missed before anything was")
	}
	for i := range missedKept + 2 {
		Missed("alarm", "", now.Add(-time.Duration(10-i)*time.Hour))
	}
	Missed("timer", "Pasta", now.Add(-time.Minute))
	got := Missing()
	if len(got) != missedKept {
		t.Fatalf("kept %d, want %d", len(got), missedKept)
	}
	if last := got[len(got)-1]; last.Says() != `timer "Pasta"` {
		t.Errorf("latest says %q", last.Says())
	}

	now = now.Add(missedFor)
	if Missing() != nil {
		t.Error("still saying it after missedFor")
	}

	Missed("alarm", "", now)
	ClearMissed()
	if Missing() != nil {
		t.Error("still saying it after it was cleared")
	}
}
