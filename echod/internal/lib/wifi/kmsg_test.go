package wifi

import (
	"io"
	"strings"
	"syscall"
	"testing"
)

// records is /dev/kmsg as a reader: one record a read, an EPIPE where one was overwritten.
type records struct{ recs []any }

func (r *records) Read(b []byte) (int, error) {
	if len(r.recs) == 0 {
		return 0, io.EOF
	}
	next := r.recs[0]
	r.recs = r.recs[1:]
	if err, ok := next.(error); ok {
		return 0, err
	}
	return copy(b, next.(string)), nil
}

func TestTheKernelsLinkLinesAreKeptAsTheyAreLogged(t *testing.T) {
	kernelTail.Lock()
	kernelTail.lines = nil
	kernelTail.Unlock()

	var recs []any
	recs = append(recs, "6,100,5000,-;wlan0: some other line\n", syscall.EPIPE)
	for i := range 10 {
		recs = append(recs, "6,"+string(rune('a'+i))+",5001,-;RSN: handshake "+string(rune('a'+i))+" [ssid:Home Net]\n SUBSYSTEM=net\n")
	}
	followKmsg(&records{recs: recs})

	got := kernelLines()
	if len(got) != kernelKept {
		t.Fatalf("kept %d lines, want %d: %q", len(got), kernelKept, got)
	}
	if !strings.HasSuffix(got[len(got)-1], "handshake j [ssid:<ssid>]") || !strings.Contains(got[0], "handshake c") {
		t.Errorf("not the last %d, oldest first: %q", kernelKept, got)
	}
	for _, l := range got {
		if strings.Contains(l, "Home Net") || strings.Contains(l, "SUBSYSTEM") {
			t.Errorf("kept %q", l)
		}
	}
}

func TestAKmsgRecordsMessageIsItsFirstLine(t *testing.T) {
	if got := kmsgMessage("6,1,2,-;hello there\n KEY=value\n"); got != "hello there" {
		t.Errorf("got %q", got)
	}
	if got := kmsgMessage("no prefix"); got != "" {
		t.Errorf("got %q", got)
	}
}
