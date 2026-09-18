//go:build !dot && !spot

package display

import "testing"

// An alarm's time on the Show's alarm list follows the clock format too.
func TestAlarmTimeFollowsClockFormat(t *testing.T) {
	defer clock24.Store(false)
	clock24.Store(false)
	if got := clockTime(15, 4); got != "3:04 PM" {
		t.Errorf("12-hour clockTime(15, 4) = %q, want %q", got, "3:04 PM")
	}
	clock24.Store(true)
	if got := clockTime(15, 4); got != "15:04" {
		t.Errorf("24-hour clockTime(15, 4) = %q, want %q", got, "15:04")
	}
}
