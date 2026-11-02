//go:build !dot && !spot

package display

import (
	"strings"
	"testing"
)

func TestCalendarListText(t *testing.T) {
	if got := calendarListText(nil); got != "none" {
		t.Errorf("empty list: %q", got)
	}
	if got := calendarListText([]string{"calendar.a", "calendar.b"}); got != "calendar.a, calendar.b" {
		t.Errorf("short list: %q", got)
	}
	var ids []string
	for i := 0; i < 40; i++ {
		ids = append(ids, "calendar.family_member_"+strings.Repeat("x", 10))
	}
	got := calendarListText(ids)
	if len(got) > 255 || !strings.Contains(got, " more") || !strings.HasPrefix(got, "calendar.family_member_") {
		t.Errorf("long list not cut to a state: %d %q", len(got), got)
	}
}
