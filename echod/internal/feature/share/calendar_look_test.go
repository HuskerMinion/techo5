package share

import (
	"slices"
	"testing"
)

// The calendar's look is shared under Calendar, all eight of its switches and the view it opens on; which calendars a screen
// hides is not a setting to share at all (it has no entity), and nor is which calendars it shows.
func TestTheCalendarLookIsShared(t *testing.T) {
	look := []string{"calendar_opens_on", "calendar_day_timeline", "calendar_coming_up_refined", "calendar_month_details",
		"calendar_event_icons", "calendar_countdowns", "calendar_week_view", "calendar_next_up", "calendar_weather"}
	for _, id := range look {
		if !Shareable(id) {
			t.Errorf("%s is not on the share list", id)
		}
	}
	for _, g := range groups {
		if g.Name == "Calendar" {
			for _, id := range look {
				if !slices.Contains(g.IDs, id) {
					t.Errorf("%s is not under Calendar", id)
				}
			}
		}
	}
	for _, id := range []string{"calendar_hidden", "calendar_sources", "calendar_popups"} {
		if Shareable(id) {
			t.Errorf("%s is on the share list", id)
		}
	}
}
