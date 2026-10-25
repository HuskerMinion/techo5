package all

import (
	"slices"
	"strings"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// The machine the tests run on has no amplifier, but may have an I2C bus that cannot say so: the
// speaker gain is left out, as on a unit whose mixer has said it has none.
func init() { speaker.GainPossible = func() bool { return false } }

// registered is the base inventory of entities, by object id.
//
// This is an inventory, not a contract: renaming one is fine, and the list is meant to be edited
// deliberately when that happens. What it catches is a component that stopped registering — which
// costs nothing at build time and shows up as an entity quietly missing from Home Assistant.
var registered = []string{
	"alarm_snooze",
	"alarm_snooze_length",
	"alarm_sound",
	"alarm_sounding",
	"alarm_stop",
	"assistant_replies_into_calls",
	"alarm_words",
	"alarm_gets_louder",
	"ambient_noise",
	"ambient_noise_sensor",
	"bass",
	"ble_advertisements",
	"bluetooth_audio",
	"bluetooth_disconnect",
	"bluetooth_forget",
	"bluetooth_forget_phones",
	"bluetooth_pairing",
	"bluetooth_phone_pairing",
	"bluetooth_proxy",
	"bluetooth_reconnect",
	"bluetooth_reconnect_phone",
	"bluetooth_secure_connections",
	"bluetooth_sets_volume",
	"button_action",
	"button_mute",
	"button_volume_down",
	"button_volume_up",
	"cached_data",
	"calendar_popup_all_day",
	"calendar_popup_before",
	"calendar_popup_chime",
	"calendar_popups",
	"camera_live",
	"camera_sound",
	"camera_web_access",
	"cancel_request",
	"check_for_updates",
	"clap_gesture",
	"confirm_threshold",
	"confirm_wake_with_home_assistant",
	"cpu_cores",
	"cpu_cores_online",
	"cpu_temperature",
	"dlna",
	"duck_on_near_miss",
	"failure_effect",
	"firmware",
	"follow_up_1",
	"follow_up_2",
	"follow_up_tone_1",
	"follow_up_tone_2",
	"follow_ups_1",
	"follow_ups_2",
	"free_space",
	"hardware_color",
	"hangup_word",
	"hangup_word_threshold",
	"headphones",
	"hear_call_under_assistant",
	"phone_plain_sip",
	"call_contacts_by_voice",
	"home_assistant_sounds",
	"insecure_tls",
	"intercom_do_not_disturb",
	"intercom_drop_in",
	"ip_address",
	"keep_recordings_1",
	"keep_recordings_2",
	"keep_wake_clips",
	"last_boot",
	"last_heard",
	"last_reply",
	"last_wake_score",
	"last_wake_word",
	"load_average",
	"lux",
	"lyrics",
	"max_listen_1",
	"max_listen_2",
	"max_think_1",
	"max_think_2",
	"media_duck_level",
	"media_on_turn",
	"memory_available",
	"metrics_interval",
	"mic_mute",
	"microphone_cancel_echo",
	"microphone_echo_canceller",
	"microphone_end_of_speech",
	"microphone_gain",
	"microphone_leveling",
	"microphone_mixing",
	"microphone_noise_reduction",
	"microphone_sensitivity",
	"min_cores",
	"missed_ring",
	"mute_led_brightness",
	"nearest_device_answers",
	"nearest_device_offset",
	"nearest_device_window",
	"next_alarm",
	"next_song_word",
	"next_song_word_threshold",
	"night_volume",
	"noise_layer_1",
	"noise_layer_2",
	"now_playing_album",
	"now_playing_artist",
	"now_playing_follows",
	"now_playing_source",
	"now_playing_state",
	"now_playing_title",
	"phone",
	"phone_answer",
	"phone_hangup",
	"phone_peer",
	"phone_ring_sound",
	"purge_cache",
	"quiet_hours",
	"radar_source",
	"radio_artist",
	"radio_station",
	"radio_temperature",
	"radio_title",
	"remote_api",
	"reply_buffer_1",
	"reply_buffer_2",
	"reply_delivery_1",
	"reply_delivery_2",
	"replying_effect_1",
	"replying_effect_2",
	"restart",
	"ring",
	"ring_muted",
	"ring_volume",
	"room_floor",
	"room_level",
	"room_reaction",
	"screen",
	"screen_answer_time",
	"screen_at_night",
	"screen_auto_brightness",
	"screen_auto_brightness_dimmest",
	"screen_call_button",
	"screen_camera_time",
	"screen_clock_format",
	"screen_clock_position",
	"screen_clock_style",
	"screen_dashboard",
	"screen_dashboard_idle",
	"screen_dashboard_kiosk",
	"screen_dashboard_view",
	"screen_font",
	"screen_date_color",
	"screen_weather_position",
	"screen_ignores_room_lights",
	"screen_language",
	"screen_light_theme",
	"screen_night_clock_style",
	"screen_night_end",
	"screen_night_hours",
	"screen_night_light_level",
	"screen_night_mode",
	"screen_night_start",
	"screen_night_tap_lifts",
	"screen_night_warmth",
	"screen_now_playing",
	"screen_power",
	"screen_theme",
	"screen_skin",
	"screen_theme_mode",
	"screen_timer_style",
	"screen_turn_style",
	"screen_weather_animation",
	"screen_web_access",
	"segment_1",
	"segment_10",
	"segment_11",
	"segment_12",
	"segment_2",
	"segment_3",
	"segment_4",
	"segment_5",
	"segment_6",
	"segment_7",
	"segment_8",
	"segment_9",
	"sendspin",
	"sendspin_state",
	"settings_lock",
	"settings_sharing",
	"setup_page",
	"sleep_timer",
	"slideshow_folder",
	"slideshow_interval",
	"slideshow_mode",
	"slideshow_screensaver_idle",
	"slideshow_screensaver_overlay",
	"slideshow_shuffle",
	"slideshow_subfolders",
	"slideshow_weather_art",
	"slideshow_whole_photo",
	"slideshow_caption",
	"slideshow_pairs",
	"immich_favorites",
	"immich_from",
	"immich_person",
	"immich_to",
	"sounding",
	"sounding_what",
	"speaker",
	"speaker_eq",
	"stop_word_sensitivity",
	"talk_back",
	"tessera_screen",
	"test_playback",
	"thinking_effect_1",
	"thinking_effect_2",
	"timer_sounding",
	"timers",
	"treble",
	"update_automatically",
	"update_channel",
	"update_outcome",
	"update_status",
	"voice_resampling",
	"volume_down_word",
	"volume_down_word_threshold",
	"volume_up_word",
	"volume_up_word_threshold",
	"wake_assistant_1",
	"wake_assistant_2",
	"wake_clips",
	"wake_clips_by_kind",
	"wake_effect_1",
	"wake_effect_2",
	"wake_light_brightness",
	"wake_light_color",
	"wake_threshold_1",
	"wake_word_during_calls",
	"wake_threshold_2",
	"wake_tone_1",
	"wake_tone_2",
	"wake_word_on_home_assistant",
	"weather_alerts",
	"weather_source",
	"wifi_received",
	"wifi_sent",
	"wifi_signal",
	"screen_clock_tap",
	"screen_clock_style_swipe",
	"screen_volume_swipe",
	"screen_dashboard_return",
	"screen_dashboard_tiles",
	"calendar_opens_on",
	"calendar_day_timeline",
	"calendar_coming_up_refined",
	"calendar_month_details",
	"calendar_event_icons",
	"calendar_countdowns",
	"calendar_week_view",
	"calendar_next_up",
	"calendar_weather",
}

func TestEveryComponentStillRegisters(t *testing.T) {
	var got []string
	for _, e := range component.Default().Entities() {
		got = append(got, objectID(e))
	}
	slices.Sort(got)

	want := slices.DeleteFunc(slices.Clone(registered), func(id string) bool { return slices.Contains(notOnThisDevice, id) })
	want = append(want, deviceSpecific...)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("registered entities changed\n got: %s\nwant: %s",
			strings.Join(got, " "), strings.Join(want, " "))
	}
}

// Two components claiming one object id is a collision Home Assistant resolves by keeping one of
// them, silently.
func TestNoDuplicateObjectIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range component.Default().Entities() {
		id := objectID(e)
		if seen[id] {
			t.Errorf("two components registered %q", id)
		}
		seen[id] = true
	}
}

// Every component has a name, which is what its log lines and its status are keyed on.
func TestEveryComponentIsNamed(t *testing.T) {
	for _, c := range component.Default().All() {
		if c.Name() == "" {
			t.Errorf("%T has no name", c)
		}
	}
}

// objectID reads the id off whatever kind of entity it is, through the Base every one embeds.
func objectID(e esphome.Entity) string {
	type based interface{ Object() string }
	if b, ok := e.(based); ok {
		return b.Object()
	}
	return ""
}
