//go:build !dot && !spot

package touch

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/input"
)

func TestRepeatedContactsKeepUnchangedSlotCoordinates(t *testing.T) {
	var events []byte
	event := func(typ, code uint16, value int32) {
		long := strconv.IntSize / 8
		b := make([]byte, 2*long+8)
		binary.LittleEndian.PutUint16(b[2*long:], typ)
		binary.LittleEndian.PutUint16(b[2*long+2:], code)
		binary.LittleEndian.PutUint32(b[2*long+4:], uint32(value))
		events = append(events, b...)
	}
	contact := func(id int32, x, y *int32) {
		event(input.EvAbs, absMTTrackingID, id)
		if x != nil {
			event(input.EvAbs, absMTPositionX, *x)
		}
		if y != nil {
			event(input.EvAbs, absMTPositionY, *y)
		}
		event(input.EvSyn, synReport, 0)
		event(input.EvAbs, absMTTrackingID, -1)
		event(input.EvSyn, synReport, 0)
	}
	x, y := int32(100), int32(200)
	contact(1, &x, &y)
	contact(2, nil, nil)
	x = 110
	contact(3, &x, nil)
	y = 210
	contact(4, nil, &y)
	path := filepath.Join(t.TempDir(), "events")
	if err := os.WriteFile(path, events, 0600); err != nil {
		t.Fatal(err)
	}
	dev, err := input.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()
	s := &Screen{dev: dev, rawW: 480, rawH: 960}
	var got []Gesture
	s.Gestures.Listen(func(g Gesture) { got = append(got, g) })
	_ = s.Run(context.Background()) // End of the finite event sample.
	want := []Gesture{{Kind: Tap, X: 200, Y: 379}, {Kind: Tap, X: 200, Y: 379}, {Kind: Tap, X: 200, Y: 369}, {Kind: Tap, X: 210, Y: 369}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("contact %d: got %v, want %v", i, got[i], want[i])
		}
	}
}
