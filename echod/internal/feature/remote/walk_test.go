package remote_test

import (
	"regexp"
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	_ "github.com/HuskerMinion/techo5/echod/internal/component/all"
	"github.com/HuskerMinion/techo5/echod/internal/feature/remote"
)

// privacyish is a name that says an entity is about privacy, security, what the device lets in or what
// it runs: a word of it (between underscores) or a phrase in it.
var privacyish = regexp.MustCompile(`(^|_)(ssh|remote|adb|tls|lock|talk|clips|recordings?|presence|gestures?|proxy|` +
	`pairing|forget|airplay|spotify|dlna|video|sendspin|phone|update|restart|purge|reset|wipe|install|listen|` +
	`wake_assistant|wake_threshold|confirm_threshold|noise|claps?|stream|streaming|nearest)(_|$)|` +
	`web_access|setup_page|baby_monitor|drop_in|camera_live|camera_dot|mic_mute|on_home_assistant|with_home_assistant|hold_gesture|voice_text|wifi_quiet`)

// harmless is every entity whose name says privacy or security and that is not: the API may change it.
var harmless = []string{
	"airplay_sets_volume",            // whose volume a sender's slider is, with AirPlay itself read-only
	"phone_ring_sound",               // how a call rings
	"bluetooth_reconnect_phone",      // reconnect the phone already paired
	"microphone_noise_reduction",     // how much the microphones' own noise is taken out
	"noise_layer_1", "noise_layer_2", // which sound a slot's noise layer plays
	"nearest_device_window", "nearest_device_offset", // timing and evening out, with the switch read-only
}

// Every entity the device has whose name says privacy or security is read-only through the API, so one
// added later is not writable just because nobody remembered the list.
func TestNothingAboutPrivacyIsWritable(t *testing.T) {
	n := 0
	foundASP := false
	for _, e := range component.Default().Entities() {
		n++
		if e.Object() == "microphone_asp_selected" {
			foundASP = true
			if remote.Writable(e) {
				t.Error("production microphone ASP select is writable through the remote API")
			}
		}
		if privacyish.MatchString(e.Object()) && remote.Writable(e) && !slices.Contains(harmless, e.Object()) {
			t.Errorf("%s is writable through the remote API: add it to readOnly in entities.go", e.Object())
		}
	}
	if n < 50 {
		t.Fatalf("only %d entities registered; the walk is broken", n)
	}
	if !foundASP {
		t.Fatal("microphone_asp_selected is missing from the production entity registry")
	}
}
