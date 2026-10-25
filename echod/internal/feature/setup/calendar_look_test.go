package setup

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The calendar's look is a choice per switch on the Weather & Calendar tab. A save changes only what
// was changed, so a default shown on the form is not kept as chosen; a choice the switch does not have
// is refused, and a device without a calendar page shows none of it.
func TestTheCalendarLookSection(t *testing.T) {
	now := map[string]int{"week_view": 1, "event_icons": 0}
	chose := map[string]int{}
	choice := func(key string) CalendarChoice {
		return CalendarChoice{Key: key, Label: key, Note: "a note", Options: []string{"On", "Off"},
			Now: func() int { return now[key] }, Choose: func(i int) { chose[key], now[key] = i, i }}
	}
	SetScreen(&ScreenChoices{Calendar: []CalendarChoice{choice("week_view"), choice("event_icons")}})
	t.Cleanup(func() { SetScreen(nil) })

	w := httptest.NewRecorder()
	calendarLookSection(w, "tok")
	page := w.Body.String()
	if !strings.Contains(page, `name="look-week_view"`) || !strings.Contains(page, `<option value="1" selected>Off</option>`) {
		t.Fatalf("the section: %s", page)
	}

	// The form as it came: nothing changed, nothing chosen.
	if p := saveCalendarLook(form(url.Values{"look-week_view": {"1"}, "look-event_icons": {"0"}})); p != "" || len(chose) != 0 {
		t.Errorf("an unchanged form chose %v (%s)", chose, p)
	}
	if p := saveCalendarLook(form(url.Values{"look-week_view": {"0"}, "look-event_icons": {"0"}})); p != "" || len(chose) != 1 || chose["week_view"] != 0 {
		t.Errorf("turning the week view on chose %v (%s)", chose, p)
	}
	for _, bad := range []url.Values{{"look-week_view": {"2"}}, {"look-week_view": {"x"}}} {
		if p := saveCalendarLook(form(bad)); p == "" {
			t.Errorf("%v was saved", bad)
		}
	}

	SetScreen(&ScreenChoices{})
	w = httptest.NewRecorder()
	calendarLookSection(w, "tok")
	if w.Body.Len() != 0 || saveCalendarLook(form(url.Values{})) == "" {
		t.Error("a screen without a calendar page has the section")
	}
}
