//go:build !dot && !spot

package display

import "testing"

// The calendar's look rows are open by name, not by their prefix: each name in everyday is a row
// lookSwitches draws, and a row added there later stays behind the PIN until it is named.
func TestTheCalendarLookRowsAreOpenByName(t *testing.T) {
	named := 0
	for _, s := range lookSwitches {
		if everyday(lookRowPrefix + s.id) {
			named++
		}
	}
	if named != len(lookSwitches) {
		t.Errorf("%d of the %d calendar look rows are open; name each in everyday, or say why one is not", named, len(lookSwitches))
	}
	if everyday(lookRowPrefix + "a_row_added_later") {
		t.Error("a calendar look row nobody named is open")
	}
}
