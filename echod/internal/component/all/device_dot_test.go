//go:build dot

package all

// notOnThisDevice is what the Echo Dot does not put up: it has no screen and no camera.
var notOnThisDevice = []string{
	"calendar_popup_all_day", "calendar_popup_before", "calendar_popup_chime", "calendar_popups",
	"camera_web_access", "radar_source", "screen", "screen_auto_brightness", "screen_call_button", "screen_camera_time", "screen_clock_format", "screen_language",
	"screen_now_playing", "screen_weather_animation", "screen_at_night", "screen_night_clock_style", "screen_night_end", "screen_night_hours", "screen_night_light_level", "screen_night_start", "screen_dashboard", "screen_dashboard_idle", "screen_dashboard_kiosk", "screen_dashboard_view",
	"screen_web_access",
	"slideshow_folder", "slideshow_interval",
	"slideshow_mode", "slideshow_screensaver_idle", "slideshow_screensaver_overlay",
	"slideshow_shuffle", "slideshow_subfolders", "weather_alerts",
}
