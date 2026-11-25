package detect

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// The log line comes every ten minutes, once twenty of the 30 s windows have been added up, and
// reports all of them: the entities still get every window.
func TestFrameTallyLogsEveryTenMinutes(t *testing.T) {
	start := time.Unix(1000, 0)
	tally := newFrameTally(start)
	windows := int(framesLogEvery / framesEvery)
	if windows != 20 {
		t.Fatalf("windows per line = %d, want 20", windows)
	}

	for i := 1; i <= windows; i++ {
		now := start.Add(time.Duration(i) * framesEvery)
		tally.add(1500, 300*time.Millisecond, time.Duration(i)*time.Millisecond,
			map[wake.Kind]time.Duration{"micro": 150 * time.Millisecond})
		if due := tally.due(now); due != (i == windows) {
			t.Fatalf("window %d: due = %v", i, due)
		}
	}

	got := map[string]any{}
	attrs := tally.attrs(start.Add(framesLogEvery))
	for i := 0; i+1 < len(attrs); i += 2 {
		got[attrs[i].(string)] = attrs[i+1]
	}
	if got["per_second"] != 50.0 {
		t.Errorf("per_second = %v, want 50", got["per_second"])
	}
	if got["mean_us"] != int64(200) {
		t.Errorf("mean_us = %v, want 200", got["mean_us"])
	}
	if got["worst_ms"] != int64(20) {
		t.Errorf("worst_ms = %v, want the worst of every window, 20", got["worst_ms"])
	}
	if got["busy_pct"] != 1 {
		t.Errorf("busy_pct = %v, want 1", got["busy_pct"])
	}
	if got["micro_us"] != int64(100) {
		t.Errorf("micro_us = %v, want 100", got["micro_us"])
	}
}
