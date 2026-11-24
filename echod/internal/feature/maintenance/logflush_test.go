package maintenance

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeLog struct {
	n     int64
	syncs int
}

func (f *fakeLog) size() int64 { return f.n }
func (f *fakeLog) sync()       { f.syncs++ }

// The period's flush only syncs a log that changed since the last one, and what was in it before the
// daemon started does not count.
func TestLogFlushOnlyWhenGrown(t *testing.T) {
	f := &fakeLog{n: 500}
	l := newLogFlush(f)

	l.flush()
	if f.syncs != 0 {
		t.Fatalf("synced a log that had not grown: %d", f.syncs)
	}
	f.n = 600
	l.flush()
	if f.syncs != 1 {
		t.Fatalf("grown log: %d syncs, want 1", f.syncs)
	}
	l.flush()
	if f.syncs != 1 {
		t.Fatalf("synced again with nothing new: %d", f.syncs)
	}
	// Emptied in place by the Dot's techo5-logs.
	f.n = 40
	l.flush()
	if f.syncs != 2 {
		t.Fatalf("emptied log: %d syncs, want 2", f.syncs)
	}
}

// A warning is flushed at once; another inside urgentGap waits out the rest of the gap.
func TestLogFlushWarnedAtOnceThenSpaced(t *testing.T) {
	f := &fakeLog{}
	l := newLogFlush(f)
	now := time.Unix(1000, 0)

	f.n = 100
	if wait := l.warned(now); wait != 0 || f.syncs != 1 {
		t.Fatalf("first warning: wait %v, %d syncs; want at once", wait, f.syncs)
	}

	f.n = 200
	if wait := l.warned(now.Add(2 * time.Second)); wait != 3*time.Second || f.syncs != 1 {
		t.Fatalf("warning 2 s later: wait %v, %d syncs; want 3 s and none", wait, f.syncs)
	}
	if wait := l.warned(now.Add(urgentGap)); wait != 0 || f.syncs != 2 {
		t.Fatalf("after the gap: wait %v, %d syncs; want at once", wait, f.syncs)
	}

	// A warning whose line some earlier flush already covered costs nothing.
	if wait := l.warned(now.Add(time.Minute)); wait != 0 || f.syncs != 2 {
		t.Fatalf("nothing new: wait %v, %d syncs", wait, f.syncs)
	}
}

// Without a log file (Fire OS's init and the Panel's app give /dev/null or a pipe) nothing happens.
func TestLogFlushWithoutAFile(t *testing.T) {
	l := newLogFlush(nil)
	l.flush()
	if wait := l.warned(time.Now()); wait != 0 {
		t.Fatalf("wait %v", wait)
	}
}

// stdLog keeps standard output and error only when they are regular files, and one of them when they
// are the same file, as techo5-run's ">> $LOG 2>&1" makes them.
func TestStdLogRegularFilesOnly(t *testing.T) {
	oldOut, oldErr := os.Stdout, os.Stderr
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()

	f, err := os.Create(filepath.Join(t.TempDir(), "techo5.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()

	os.Stdout, os.Stderr = null, null
	if got := openStdLog(); got != nil {
		t.Fatalf("/dev/null kept: %v", got)
	}

	os.Stdout, os.Stderr = f, f
	got, ok := openStdLog().(stdLog)
	if !ok || len(got) != 1 {
		t.Fatalf("same file twice: %v", got)
	}
	if _, err := f.WriteString("W a line\n"); err != nil {
		t.Fatal(err)
	}
	if got.size() != 9 {
		t.Fatalf("size %d, want 9", got.size())
	}
	got.sync()
}
