//go:build !dot

package display

import "fmt"

// optionLabel translates known generic labels at the presentation boundary.
// Canonical tables and Home Assistant option values remain unchanged.
func optionLabel(english string) string {
	switch english {
	case "Nothing":
		return tr("option_nothing")
	case "Its words":
		return tr("option_its_words")
	case "The time and its words":
		return tr("option_the_time_and_its_words")
	case "Full at once":
		return tr("option_full_at_once")
	case "Over 30 seconds":
		return tr("option_over_30_seconds")
	case "Over 1 minute":
		return tr("option_over_1_minute")
	case "Over 2 minutes":
		return tr("option_over_2_minutes")
	case "Over 5 minutes":
		return tr("option_over_5_minutes")

	case "Bottom":
		return tr("option_bottom")
	case "Center":
		return tr("option_center")
	case "Bottom left":
		return tr("option_bottom_left")
	case "Bottom center":
		return tr("option_bottom_center")
	case "Bottom right":
		return tr("option_bottom_right")
	case "Top left":
		return tr("option_top_left")
	case "Top right":
		return tr("option_top_right")
	case "Default":
		return tr("option_default")
	case "White":
		return tr("option_white")
	case "Gold":
		return tr("option_gold")
	case "Sky":
		return tr("option_sky")
	case "Mint":
		return tr("option_mint")
	case "Rose":
		return tr("option_rose")
	case "Whole picture":
		return tr("option_whole_picture")
	case "Fill the screen":
		return tr("option_fill_the_screen")
	case "Camera's own size":
		return tr("option_camera_s_own_size")
	case "Solid":
		return tr("option_solid")
	case "Fade":
		return tr("option_fade")
	case "Light":
		return tr("option_light")
	case "Medium":
		return tr("option_medium")
	case "Strong":
		return tr("option_strong")
	case "Dim":
		return tr("option_dim")
	case "Screen off":
		return tr("option_screen_off")
	case "Night light":
		return tr("option_night_light")
	case "Night clock":
		return tr("option_night_clock")
	case "Red":
		return tr("option_red")
	case "Red LED":
		return tr("option_red_led")
	case "Full page":
		return tr("option_full_page")
	case "Strip":
		return tr("option_strip")
	case "Full":
		return tr("option_full")
	case "Simple":
		return tr("option_simple")
	case "Day":
		return tr("option_day")
	case "Week":
		return tr("option_week")
	case "Month":
		return tr("option_month")
	case "Coming up":
		return tr("option_coming_up")
	case "Automatic":
		return tr("option_automatic")
	case "Never":
		return tr("option_never")
	case "Always":
		return tr("option_always")
	case "Show":
		return tr("option_show")
	case "Hide":
		return tr("option_hide")
	case "None":
		return tr("option_none")
	case "Off":
		return tr("option_off")
	case "On":
		return tr("option_on")
	case "Headphones":
		return tr("option_headphones")
	case "Speaker":
		return tr("option_speaker")
	case "Both":
		return tr("option_both")
	case "Silent":
		return tr("option_silent")
	case "Once":
		return tr("option_once")
	case "Every day":
		return tr("option_every_day")
	case "Weekdays":
		return tr("option_weekdays")
	case "Weekends":
		return tr("option_weekends")
	case "5 seconds":
		return tr("option_5_seconds")
	case "10 seconds":
		return tr("option_10_seconds")
	case "15 seconds":
		return tr("option_15_seconds")
	case "30 seconds":
		return tr("option_30_seconds")
	case "1 minute":
		return tr("option_1_minute")
	case "2 minutes":
		return tr("option_2_minutes")
	case "5 minutes":
		return tr("option_5_minutes")
	case "10 minutes":
		return tr("option_10_minutes")
	case "Until tapped":
		return tr("option_until_tapped")
	case "1 hour":
		return tr("option_1_hour")
	case "12-hour":
		return tr("option_12_hour")
	case "24-hour":
		return tr("option_24_hour")
	case "Strip after 10 s":
		return tr("option_strip_after_10_s")
	case "Strip after 30 s":
		return tr("option_strip_after_30_s")
	case "Strip at once":
		return tr("option_strip_at_once")
	case "As it starts":
		return tr("option_as_it_starts")
	case "1 hour before":
		return tr("option_1_hour_before")
	case "In the morning":
		return tr("option_in_the_morning")
	case "Internal speaker":
		return tr("option_internal_speaker")
	case "Headphones / AUX":
		return tr("option_headphones_aux")
	case "Speaker and headphones":
		return tr("option_speaker_and_headphones")

	}

	var minutes int
	if _, err := fmt.Sscanf(english, "%d minutes before", &minutes); err == nil && english == fmt.Sprintf("%d minutes before", minutes) {
		return fmt.Sprintf(tr("minutes_before"), minutes)
	}
	if _, err := fmt.Sscanf(english, "%d minutes", &minutes); err == nil && english == fmt.Sprintf("%d minutes", minutes) {
		return fmt.Sprintf(tr("ui_d_minutes"), minutes)
	}
	if _, err := fmt.Sscanf(english, "%d seconds", &minutes); err == nil && english == fmt.Sprintf("%d seconds", minutes) {
		return fmt.Sprintf(tr("seconds_option"), minutes)
	}
	return english
}

func builtinOptionRow(id string) bool {
	switch id {
	case "photoevery", "clocktap", "alarmwords", "alarmrise", "clock", "clockpos", "weatherpos", "datecolor", "camtime", "camfit", "cambands", "camshade", "answertime", "timerstyle", "turnstyle", "musicstrip", "atnight", "nightstyle", "radarsrc", "output", "cal_open", "cal_opens_on", "cal_month_details", "calpopallday", "calpopwhen":
		return true
	}
	return false
}

func pickerFor(id string, sv sheetView) (pickerView, bool) {
	p, ok := pickerForRaw(id, sv)
	if builtinOptionRow(id) {
		opts := make([]string, len(p.opts))
		for i, label := range p.opts {
			opts[i] = optionLabel(label)
		}
		p.opts = opts
	}
	return p, ok
}
