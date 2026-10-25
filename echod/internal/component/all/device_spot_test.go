//go:build spot

package all

// notOnThisDevice: what the Echo Spot build leaves out of the shared list: the LED ring's entities, as
// on the Show, and the Show's own screen settings.
var notOnThisDevice = append(ringOnly, "calendar_popup_all_day", "calendar_popup_before", "calendar_popup_chime", "calendar_popups", "screen_now_playing", "screen_theme", "screen_theme_mode", "screen_timer_style", "screen_night_warmth", "screen_light_theme", "screen_auto_brightness_dimmest", "screen_date_color", "screen_weather_position", "screen_clock_tap", "screen_dashboard_tiles", "lyrics", "screen_night_tap_lifts", "slideshow_pairs",
	// Tessera screens are the Show's for now (feature/tessera): a round screen wants a layout of its own.
	"tessera_screen",
	// The Show's calendar page and its look; the Spot's calendar is its own.
	"calendar_opens_on", "calendar_day_timeline", "calendar_coming_up_refined", "calendar_month_details", "calendar_event_icons", "calendar_countdowns", "calendar_week_view", "calendar_next_up", "calendar_weather")

// deviceSpecific: the audio output, and the subtle muted ring, since only the Spot draws its mute on
// the screen; the video player (feature/video), which the Show has too and the Dot does not; and the
// AirPlay and Spotify Connect receivers (feature/streaming), as on the Show and the Dot.
var deviceSpecific = []string{"audio_output", "screen_mute_ring_subtle", "video", "dlna_video", "video_state", "video_title", "video_error", "airplay", "spotify_connect"}

// actionsNotOnThisDevice: the Spot offers every action the shared code has.
var actionsNotOnThisDevice = map[string]string{}
