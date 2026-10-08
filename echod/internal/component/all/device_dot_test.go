//go:build dot

package all

// notOnThisDevice is what the Echo Dot does not put up: it has no screen and no camera.
var notOnThisDevice = []string{
	"calendar_popup_all_day", "calendar_popup_before", "calendar_popup_chime", "calendar_popups",
	"camera_sound", "camera_web_access", "lyrics", "now_playing_follows", "radar_source", "screen", "screen_answer_time", "screen_auto_brightness", "screen_call_button", "screen_camera_time", "screen_clock_format", "screen_clock_position", "screen_clock_style", "screen_date_color", "screen_language", "screen_turn_style",
	"screen_now_playing", "screen_theme", "screen_weather_animation", "screen_at_night", "screen_night_clock_style", "screen_night_end", "screen_night_hours", "screen_night_light_level", "screen_auto_brightness_dimmest", "screen_night_mode", "screen_night_start", "screen_dashboard", "screen_dashboard_idle", "screen_dashboard_kiosk", "screen_dashboard_view",
	"screen_web_access", "settings_lock",
	"slideshow_folder", "slideshow_interval",
	"slideshow_mode", "slideshow_screensaver_idle", "slideshow_screensaver_overlay",
	"slideshow_shuffle", "slideshow_subfolders", "slideshow_weather_art", "slideshow_whole_photo", "talk_back", "weather_alerts",
	"screen_clock_tap", "screen_dashboard_return", "screen_dashboard_tiles", "screen_clock_style_swipe",
}

var deviceSpecific = []string{"airplay", "audio_output", "spotify_connect"}
