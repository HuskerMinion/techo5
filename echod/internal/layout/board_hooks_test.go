//go:build !dot && !spot && !panel

package layout

import "testing"

// A board's own idme answers first, for every field, and nothing is read from the eMMC for it.
func TestABoardIdmeAnswersFirst(t *testing.T) {
	defer func(f func(string) (string, bool)) { boardIdme = f }(boardIdme)
	boardIdme = func(name string) (string, bool) {
		if name == "mac_addr" {
			return "02:00:00:00:00:01", true
		}
		return "", false
	}
	if v, ok := idmeFallback("mac_addr"); !ok || v != "02:00:00:00:00:01" {
		t.Errorf("idmeFallback(mac_addr) = %q, %v; want the board's", v, ok)
	}
	if v, ok := idmeFallback("serial"); ok {
		t.Errorf("idmeFallback(serial) = %q, want none", v)
	}
}

// With no board idme, a board that is neither the Skylight nor athena on mainline has none.
func TestNoBoardIdmeIsNone(t *testing.T) {
	if boardIdme != nil {
		t.Skip("a board of this build sets its own idme")
	}
	if Skylight() || AthenaMainline() {
		t.Skip("this board has an idme fallback of its own")
	}
	if v, ok := idmeFallback("mac_addr"); ok {
		t.Errorf("idmeFallback(mac_addr) = %q with no board idme", v)
	}
}

// SoundCard is the board's card where one is set, empty otherwise.
func TestABoardSetsItsSoundCard(t *testing.T) {
	defer func(v string) { soundCard = v }(soundCard)
	soundCard = ""
	if c := SoundCard(); c != "" {
		t.Errorf("SoundCard() = %q, want card 0's empty", c)
	}
	soundCard = "Codec"
	if c := SoundCard(); c != "Codec" {
		t.Errorf("SoundCard() = %q, want the board's Codec", c)
	}
}
