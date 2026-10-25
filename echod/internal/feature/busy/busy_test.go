package busy

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 16, 6, 0, 0, 0, time.UTC)

func TestReasons(t *testing.T) {
	for _, c := range []struct {
		name string
		s    Snapshot
		at   time.Time
		want []string
	}{
		{"idle", Snapshot{}, now, nil},
		{"idle with an alarm tomorrow", Snapshot{NextAlarm: now.Add(20 * time.Hour)}, now, nil},
		{"everything", Snapshot{Ring: true, Voice: true, Call: true, Sound: true, Media: true, Light: true, NextAlarm: now.Add(time.Minute)}, now,
			[]string{"ring", "soon", "voice", "call", "sound", "media", "light", "alarm"}},
		{"music", Snapshot{Media: true}, now, []string{"media"}},
		{"alarm in 30 minutes", Snapshot{NextAlarm: now.Add(AlarmAhead)}, now, []string{"alarm"}},
		{"alarm in a minute", Snapshot{NextAlarm: now.Add(RingAhead)}, now, []string{"soon", "alarm"}},
		{"timer in a minute", Snapshot{NextTimer: now.Add(RingAhead)}, now, []string{"soon"}},
		{"timer in two minutes", Snapshot{NextTimer: now.Add(2 * RingAhead)}, now, nil},
		{"timer with the clock not set", Snapshot{NextTimer: time.Date(1970, 1, 1, 0, 0, 30, 0, time.UTC)},
			time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), []string{"soon"}},
		{"alarm in 31 minutes", Snapshot{NextAlarm: now.Add(AlarmAhead + time.Minute)}, now, nil},
		{"alarm due a moment ago", Snapshot{NextAlarm: now.Add(-time.Second)}, now, []string{"soon", "alarm"}},
		{"clock not set", Snapshot{NextAlarm: time.Date(1970, 1, 1, 0, 10, 0, 0, time.UTC)}, time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), nil},
	} {
		if got := Reasons(c.s, c.at); !slices.Equal(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestMarker(t *testing.T) {
	p := filepath.Join(t.TempDir(), "techo5", "busy")
	up := int64(100)
	m := &Marker{path: p, pid: 1234, uptime: func() (int64, error) { return up, nil }}
	if err := m.Set(nil); err != nil {
		t.Fatalf("removing a marker that is not there: %v", err)
	}
	if err := m.Set([]string{"ring", "alarm"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "pid 1234\nat 100\nring\nalarm\n" {
		t.Fatalf("marker says %q", b)
	}
	// Nothing changed but the time: written again, so the script can tell it is fresh.
	up = 105
	if err := m.Set([]string{"ring", "alarm"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "pid 1234\nat 105\nring\nalarm\n" {
		t.Fatalf("marker says %q", b)
	}
	if err := m.Set([]string{"media"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "pid 1234\nat 105\nmedia\n" {
		t.Fatalf("marker says %q", b)
	}
	if err := m.Set(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("marker still there when idle: %v", err)
	}
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("a temporary file was left behind")
	}
}

func TestUptime(t *testing.T) {
	if _, err := os.Stat("/proc/uptime"); err != nil {
		t.Skip("no /proc/uptime here")
	}
	if up, err := uptime(); err != nil || up <= 0 {
		t.Fatalf("uptime = %d, %v", up, err)
	}
}

func TestRingsIn(t *testing.T) {
	alive := func(pid string) bool { return pid == "1234" }
	for _, c := range []struct {
		name   string
		marker string
		up     int64
		want   bool
	}{
		{"ringing", "pid 1234\nat 100\nring\n", 110, true},
		{"due within the minute", "pid 1234\nat 100\nsoon\nalarm\n", 110, true},
		{"only music", "pid 1234\nat 100\nmedia\n", 110, false},
		{"a minute old", "pid 1234\nat 100\nring\n", 160, true},
		{"stale", "pid 1234\nat 100\nring\n", 161, false},
		{"daemon gone", "pid 999\nat 100\nring\n", 110, false},
		{"no time", "pid 1234\nring\n", 110, false},
		{"empty", "", 110, false},
	} {
		if got := ringsIn(c.marker, c.up, alive); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// Run writes the marker where the boot script reads it, removes it on the way out, and leaves the path
// alone everywhere else.
func TestRunKeepsTheMarkerWhereItIsRead(t *testing.T) {
	for _, here := range []bool{true, false} {
		was := keepsMarker
		keepsMarker = func() bool { return here }
		p := filepath.Join(t.TempDir(), "busy")
		b := &Busy{
			marker: &Marker{path: p, pid: 1234, uptime: func() (int64, error) { return 100, nil }},
			look:   func(time.Time) Snapshot { return Snapshot{Ring: true} },
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); _ = b.Run(ctx) }()
		wrote := false
		for end := time.Now().Add(time.Second); time.Now().Before(end) && !wrote; time.Sleep(10 * time.Millisecond) {
			_, err := os.Stat(p)
			wrote = err == nil
		}
		cancel()
		<-done
		keepsMarker = was
		if wrote != here {
			t.Errorf("read here %v: marker written %v", here, wrote)
		}
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("read here %v: marker left behind", here)
		}
	}
}
