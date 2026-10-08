//go:build spot

package all

// notOnThisDevice: the Echo Spot build has every entity the Show's has.
var notOnThisDevice = []string{"calendar_popup_all_day", "calendar_popup_before", "calendar_popup_chime", "calendar_popups", "screen_now_playing", "screen_theme", "screen_at_night", "screen_night_clock_style", "screen_night_end", "screen_night_hours", "screen_night_light_level", "screen_auto_brightness_dimmest", "screen_clock_position", "screen_date_color", "screen_night_mode", "screen_night_start", "screen_clock_tap", "screen_dashboard_tiles", "lyrics"}

// deviceSpecific: the audio output, and the subtle muted ring, since only the Spot draws its mute on
// the screen; and the video player (feature/video), which the Show has too and the Dot does not.
var deviceSpecific = []string{"audio_output", "screen_mute_ring_subtle", "video", "dlna_video", "video_state", "video_title", "video_error"}
