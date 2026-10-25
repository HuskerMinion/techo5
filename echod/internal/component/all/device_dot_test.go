//go:build dot

package all

// notOnThisDevice is what the Echo Dot does not put up: it has no screen and no camera.
var notOnThisDevice = []string{
	"calendar_popup_all_day", "calendar_popup_before", "calendar_popup_chime", "calendar_popups",
	"camera_live", "camera_sound", "camera_web_access", "lyrics", "now_playing_follows", "radar_source", "screen", "screen_answer_time", "screen_auto_brightness", "screen_call_button", "screen_camera_time", "screen_clock_format", "screen_clock_position", "screen_clock_style", "screen_date_color", "screen_weather_position", "screen_language", "screen_skin", "screen_timer_style", "screen_turn_style", "screen_night_warmth",
	"screen_now_playing", "screen_theme", "screen_theme_mode", "screen_light_theme", "screen_weather_animation", "screen_at_night", "screen_night_clock_style", "screen_night_end", "screen_night_hours", "screen_night_light_level", "screen_auto_brightness_dimmest", "screen_night_mode", "screen_night_start", "screen_dashboard", "screen_dashboard_idle", "screen_dashboard_kiosk", "screen_dashboard_view",
	"screen_web_access", "settings_lock",
	"slideshow_folder", "slideshow_interval",
	"slideshow_mode", "slideshow_screensaver_idle", "slideshow_screensaver_overlay",
	"slideshow_shuffle", "slideshow_subfolders", "slideshow_weather_art", "slideshow_whole_photo", "slideshow_pairs", "slideshow_caption", "immich_favorites", "immich_from", "immich_person", "immich_to", "talk_back", "weather_alerts",
	"screen_clock_tap", "screen_dashboard_return", "screen_dashboard_tiles", "screen_clock_style_swipe", "screen_volume_swipe", "screen_night_tap_lifts",
	"screen_font", "screen_power", "screen_ignores_room_lights", "tessera_screen",
	"calendar_opens_on", "calendar_day_timeline", "calendar_coming_up_refined", "calendar_month_details", "calendar_event_icons", "calendar_countdowns", "calendar_week_view", "calendar_next_up", "calendar_weather",
}

// speaker_gain is the codec's driver gain on the Dot (hardware/speaker/paths_dot.go), always there:
// all_test.go's stand-in for a missing TAS5805M comes too late for the media player, built before it.
var deviceSpecific = []string{"airplay", "audio_output", "speaker_gain", "spotify_connect"}

// actionsNotOnThisDevice is what the Echo Dot must never offer, and which component would: the fixed
// network address, since the Dot's image has a techo5-net of its own that joins Wi-Fi when run and
// knows nothing of one (lib/wifi/address_dot.go).
var actionsNotOnThisDevice = map[string]string{"network_address": "wifiwatch"}
