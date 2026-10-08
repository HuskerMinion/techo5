//go:build !dot

package ambient

import (
	"errors"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/input"
)

// The sensor starts from the value the kernel kept, so a room whose light has not changed since a
// restart is not unknown. A device that cannot say leaves it unknown until the first reading, as
// before.
func TestTheSensorStartsFromItsLastValue(t *testing.T) {
	s := &Sensor{}
	if _, ok := s.seed(func(uint16) (input.AbsInfo, error) { return input.AbsInfo{}, errors.New("no axis") }); ok {
		t.Fatal("a device with no axis gave a value to start from")
	}
	if _, _, ok := s.Current(); ok {
		t.Fatal("a failed query left a reading")
	}

	var asked uint16 = 99
	lux, ok := s.seed(func(code uint16) (input.AbsInfo, error) {
		asked = code
		return input.AbsInfo{Value: 42, Max: 65535}, nil
	})
	if !ok || lux != 42 {
		t.Fatalf("seeded %v (%v), want 42", lux, ok)
	}
	if asked != 0 {
		t.Errorf("asked for axis %d, want ABS_X", asked)
	}
	if got, _, ok := s.Current(); !ok || got != 42 {
		t.Fatalf("after starting the reading is %v (%v), want 42", got, ok)
	}
}
