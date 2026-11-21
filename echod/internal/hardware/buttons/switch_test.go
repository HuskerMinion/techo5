package buttons

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/input"
)

// evdevRecords writes events as the kernel lays them out: two longs of timestamp, then type, code
// and value.
func evdevRecords(t *testing.T, events ...input.Event) *input.Device {
	t.Helper()
	long := strconv.IntSize / 8
	var b []byte
	for _, e := range events {
		rec := make([]byte, 2*long+8)
		binary.LittleEndian.PutUint16(rec[2*long:], e.Type)
		binary.LittleEndian.PutUint16(rec[2*long+2:], e.Code)
		binary.LittleEndian.PutUint32(rec[2*long+4:], uint32(e.Value))
		b = append(b, rec...)
	}
	p := filepath.Join(t.TempDir(), "event0")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := input.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// A board that names a switch in switchTaps hears each move of it, either way, as a tap of the
// button it stands for. A switch it doesn't name, and every switch on a board that names none,
// stays quiet. The switch code here (5, SW_FRONT_PROXIMITY) is made up for the test.
func TestASwitchABoardNamesIsReportedAsTaps(t *testing.T) {
	defer func(m map[uint16]Name) { switchTaps = m }(switchTaps)
	moves := []input.Event{
		{Type: input.EvSw, Code: 5, Value: 1},
		{Type: input.EvSyn},
		{Type: input.EvSw, Code: 5, Value: 0},
		{Type: input.EvSyn},
		{Type: input.EvSw, Code: 2, Value: 1}, // not named
		{Type: input.EvSyn},
	}
	run := func() []Event {
		c := &Controller{}
		var got []Event
		defer c.Events.Listen(func(e Event) { got = append(got, e) })()
		c.watch(context.Background(), evdevRecords(t, moves...)) // ends at the end of the records
		return got
	}

	switchTaps = nil
	if got := run(); len(got) != 0 {
		t.Errorf("no switches named: got %v, want nothing", got)
	}

	switchTaps = map[uint16]Name{5: Mute}
	got := run()
	if len(got) < 2 || got[0] != (Event{Mute, Tap}) || got[1] != (Event{Mute, Tap}) {
		t.Fatalf("switch 5 named as the mute: got %v, want two mute taps", got)
	}
	for _, e := range got[2:] {
		if e.Kind == Tap {
			t.Errorf("an extra tap %v", e)
		}
	}
}
