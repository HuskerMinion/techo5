package agt

import "testing"

// A length that does not fit an int must not get past the bounds check: as one it is negative, and
// the slice panics. What comes off the radio can say anything.
func TestAHugeLengthIsAnErrorNotAPanic(t *testing.T) {
	// Field 1, length-delimited, length 2^64-1, then a byte.
	huge := []byte{0x0a, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01, 0x00}
	if _, err := ParseFields(huge); err == nil {
		t.Error("a length beyond the data was taken")
	}
	if m := DecodeMessage(huge); m.Name != "" {
		t.Errorf("decoded as %q", m.Name)
	}
	// Inside the outer message too.
	nested := append([]byte{0x0a, byte(len(huge))}, huge...)
	if m := DecodeMessage(nested); m.Name != "" {
		t.Errorf("nested: decoded as %q", m.Name)
	}
}
