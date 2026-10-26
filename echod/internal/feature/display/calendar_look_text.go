//go:build !dot && !spot

package display

// calendarLookText translates only device-screen presentation. The shared
// lookSwitches metadata remains English for Home Assistant and the setup API.
func calendarLookText(s lookSwitch) (label, sub string) {
	switch s.id {
	case "opens_on":
		return tr("calendar_look_opens_on_label"), tr("calendar_look_opens_on_sub")
	case "day_timeline":
		return tr("calendar_look_day_timeline_label"), tr("calendar_look_day_timeline_sub")
	case "coming_up_refined":
		return tr("calendar_look_coming_up_refined_label"), tr("calendar_look_coming_up_refined_sub")
	case "month_details":
		return tr("calendar_look_month_details_label"), tr("calendar_look_month_details_sub")
	case "event_icons":
		return tr("calendar_look_event_icons_label"), tr("calendar_look_event_icons_sub")
	case "countdowns":
		return tr("calendar_look_countdowns_label"), tr("calendar_look_countdowns_sub")
	case "week_view":
		return tr("calendar_look_week_view_label"), tr("calendar_look_week_view_sub")
	case "next_up":
		return tr("calendar_look_next_up_label"), tr("calendar_look_next_up_sub")
	case "weather":
		return tr("calendar_look_weather_label"), tr("calendar_look_weather_sub")
	}
	return s.label, s.sub
}
