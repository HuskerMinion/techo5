package all

import (
	"slices"
	"strings"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
)

// registered is every entity the components put up, by object id.
//
// This is an inventory, not a contract: renaming one is fine, and the list is meant to be edited
// deliberately when that happens. What it catches is a component that stopped registering — which
// costs nothing at build time and shows up as an entity quietly missing from Home Assistant.
var registered = []string{
	"alarm_snooze",
	"alarm_snooze_length",
	"alarm_sound",
	"alarm_stop",
	"bass",
	"ble_advertisements",
	"bluetooth_audio",
	"bluetooth_disconnect",
	"bluetooth_pairing",
	"bluetooth_proxy",
	"bluetooth_reconnect",
	"button_action",
	"button_mute",
	"button_volume_down",
	"button_volume_up",
	"cached_data",
	"camera_web_access",
	"check_for_updates",
	"cpu_cores",
	"cpu_cores_online",
	"cpu_temperature",
	"duck_on_near_miss",
	"failure_effect",
	"firmware",
	"follow_up_1",
	"follow_up_2",
	"free_space",
	"hardware_color",
	"headphones",
	"insecure_tls",
	"ip_address",
	"keep_recordings_1",
	"keep_recordings_2",
	"last_heard",
	"last_reply",
	"last_wake_word",
	"load_average",
	"lux",
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
	"microphone_gain",
	"microphone_leveling",
	"microphone_mixing",
	"microphone_noise_reduction",
	"microphone_sensitivity",
	"min_cores",
	"mute_led_brightness",
	"next_alarm",
	"noise_layer_1",
	"noise_layer_2",
	"phone",
	"phone_answer",
	"phone_hangup",
	"phone_peer",
	"purge_cache",
	"radio_temperature",
	"reply_buffer_1",
	"reply_buffer_2",
	"reply_delivery_1",
	"reply_delivery_2",
	"replying_effect_1",
	"replying_effect_2",
	"ring",
	"ring_muted",
	"room_floor",
	"room_level",
	"room_reaction",
	"screen",
	"screen_auto_brightness",
	"screen_clock_format",
	"screen_language",
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
	"setup_page",
	"sleep_timer",
	"slideshow_folder",
	"slideshow_interval",
	"slideshow_mode",
	"slideshow_screensaver_idle",
	"slideshow_screensaver_overlay",
	"slideshow_shuffle",
	"slideshow_subfolders",
	"speaker",
	"speaker_eq",
	"stop_word_sensitivity",
	"test_playback",
	"thinking_effect_1",
	"thinking_effect_2",
	"timers",
	"treble",
	"update_channel",
	"update_outcome",
	"update_status",
	"voice_resampling",
	"wake_assistant_1",
	"wake_assistant_2",
	"wake_effect_1",
	"wake_effect_2",
	"wake_threshold_1",
	"wake_threshold_2",
	"wake_tone_1",
	"wake_tone_2",
	"weather_source",
	"wifi_received",
	"wifi_sent",
	"wifi_signal",
}

func TestEveryComponentStillRegisters(t *testing.T) {
	var got []string
	for _, e := range component.Default().Entities() {
		got = append(got, objectID(e))
	}
	slices.Sort(got)

	want := slices.DeleteFunc(slices.Clone(registered), func(id string) bool { return slices.Contains(notOnThisDevice, id) })
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
