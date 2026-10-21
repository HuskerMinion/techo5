package remote

import (
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// keptSetting is a read-only entity's setting: how a file would turn it on, and whether it is on.
type keptSetting struct {
	set func(*config.Config)
	on  func(config.Config) bool
}

// keptByRestore is every read-only entity backed by a setting, which a restored file must never turn
// on either: what this API may not change, a settings file may not change behind it.
var keptByRestore = map[string]keptSetting{
	"remote_api":                       {func(c *config.Config) { c.Remote.On = true }, func(c config.Config) bool { return c.Remote.On }},
	"ssh":                              {func(c *config.Config) { c.Security.SSH = true }, func(c config.Config) bool { return c.Security.SSH }},
	"camera_web_access":                {func(c *config.Config) { c.Security.Camera = true }, func(c config.Config) bool { return c.Security.Camera }},
	"screen_web_access":                {func(c *config.Config) { c.Security.Screen = true }, func(c config.Config) bool { return c.Security.Screen }},
	"talk_back":                        {func(c *config.Config) { c.Security.TalkBack = true }, func(c config.Config) bool { return c.Security.TalkBack }},
	"settings_lock":                    {func(c *config.Config) { c.Security.LockPIN = "2468" }, func(c config.Config) bool { return c.Security.LockPIN != "" }},
	"remote_adb":                       {func(c *config.Config) { c.Diag.RemoteADB = true }, func(c config.Config) bool { return c.Diag.RemoteADB }},
	"insecure_tls":                     {func(c *config.Config) { c.Diag.InsecureTLS = true }, func(c config.Config) bool { return c.Diag.InsecureTLS }},
	"airplay":                          {func(c *config.Config) { c.Streaming.AirPlay = true }, func(c config.Config) bool { return c.Streaming.AirPlay }},
	"spotify_connect":                  {func(c *config.Config) { c.Streaming.Spotify = true }, func(c config.Config) bool { return c.Streaming.Spotify }},
	"dlna":                             {func(c *config.Config) { c.Streaming.DLNA = true }, func(c config.Config) bool { return c.Streaming.DLNA }},
	"video":                            {func(c *config.Config) { c.Video.On = true }, func(c config.Config) bool { return c.Video.On }},
	"dlna_video":                       {func(c *config.Config) { c.Video.DLNA = true }, func(c config.Config) bool { return c.Video.DLNA }},
	"sendspin":                         {func(c *config.Config) { c.Sendspin.Enabled = !c.Sendspin.Enabled }, nil},
	"bluetooth_proxy":                  {func(c *config.Config) { c.Bluetooth.Proxy = true }, func(c config.Config) bool { return c.Bluetooth.Proxy }},
	"bluetooth_secure_connections":     {func(c *config.Config) { c.Bluetooth.SecureConnectionsOff = true }, func(c config.Config) bool { return c.Bluetooth.SecureConnectionsOff }},
	"mic_mute":                         {func(c *config.Config) { c.Microphone.Muted = !c.Microphone.Muted }, nil},
	"intercom_drop_in":                 {func(c *config.Config) { c.Home.DropIn = true }, func(c config.Config) bool { return c.Home.DropIn }},
	"wake_word_during_calls":           {func(c *config.Config) { c.Home.CallWake = true }, func(c config.Config) bool { return c.Home.CallWake }},
	"assistant_replies_into_calls":     {func(c *config.Config) { c.Home.CallReplies = true }, func(c config.Config) bool { return c.Home.CallReplies }},
	"hear_call_under_assistant":        {func(c *config.Config) { c.Home.CallUnderlay = true }, func(c config.Config) bool { return c.Home.CallUnderlay }},
	"hangup_word":                      {func(c *config.Config) { c.Wake.Hangup.ID = config.HangupStop }, func(c config.Config) bool { return c.Wake.Hangup.On() }},
	"hangup_word_threshold":            {func(c *config.Config) { c.Wake.Hangup.Threshold = 0.6 }, func(c config.Config) bool { return c.Wake.Hangup.Threshold == 0.6 }},
	"camera_live":                      {func(c *config.Config) { c.Home.CameraLive = true }, func(c config.Config) bool { return c.Home.CameraLive }},
	"keep_wake_clips":                  {func(c *config.Config) { c.Wake.KeepClips = true }, func(c config.Config) bool { return c.Wake.KeepClips }},
	"keep_recordings_1":                {setRecordings(0), recordings(0)},
	"keep_recordings_2":                {setRecordings(1), recordings(1)},
	"presence_detection":               {func(c *config.Config) { c.Presence.On = true }, func(c config.Config) bool { return c.Presence.On }},
	"gestures":                         {func(c *config.Config) { c.Presence.Gestures = true }, func(c config.Config) bool { return c.Presence.Gestures }},
	"wake_word_on_home_assistant":      {func(c *config.Config) { c.Wake.OnHA.Stream = true }, func(c config.Config) bool { return c.Wake.OnHA.Stream }},
	"confirm_wake_with_home_assistant": {func(c *config.Config) { c.Wake.OnHA.Confirm = true }, func(c config.Config) bool { return c.Wake.OnHA.Confirm }},
	"ambient_noise_sensor":             {func(c *config.Config) { c.Microphone.Noise = true }, func(c config.Config) bool { return c.Microphone.Noise }},
	"clap_gesture":                     {func(c *config.Config) { c.Microphone.Claps = true }, func(c config.Config) bool { return c.Microphone.Claps }},
	"clock_hold_gesture":               {func(c *config.Config) { c.Screen.ClockHold = true }, func(c config.Config) bool { return c.Screen.ClockHold }},
	"camera_streaming":                 {func(c *config.Config) { c.CamStream.On = true }, func(c config.Config) bool { return c.CamStream.On }},
	"nearest_device_answers":           {func(c *config.Config) { c.Nearest.Off = true }, func(c config.Config) bool { return c.Nearest.Off }},
	"person_detection":                 {func(c *config.Config) { c.Presence.Person = true }, func(c config.Config) bool { return c.Presence.Person }},
	"settings_sharing":                 {func(c *config.Config) { c.Share.On = true }, func(c config.Config) bool { return c.Share.On }},
	"update_automatically":             {func(c *config.Config) { c.Update.AutoInstall = true }, func(c config.Config) bool { return c.Update.AutoInstall }},
	"update_channel":                   {func(c *config.Config) { c.Update.Channel = "dev" }, func(c config.Config) bool { return c.Update.Channel == "dev" }},
}

// notASetting is the read-only entities with no setting behind them for a file to carry: presses,
// how readily a word wakes the device (a slot's tuning, which a restore brings with its words), and
// what is on or paired right now.
var notASetting = []string{
	"setup_page", "presence", "wake_assistant_1", "wake_assistant_2", "wake_threshold_1", "wake_threshold_2",
	"confirm_threshold", "max_listen_1", "max_listen_2", "phone", "phone_answer", "phone_hangup", "phone_peer",
	"bluetooth_pairing", "bluetooth_phone_pairing", "bluetooth_forget", "bluetooth_forget_phones",
	"check_for_updates", "restart", "purge_cache",
}

func setRecordings(slot int) func(*config.Config) {
	return func(c *config.Config) {
		for len(c.Wake.Words) <= slot {
			c.Wake.Words = append(c.Wake.Words, config.WakeWord{ID: "word"})
		}
		c.Wake.Words[slot].Recordings = 5
	}
}

func recordings(slot int) func(config.Config) bool {
	return func(c config.Config) bool { return slot < len(c.Wake.Words) && c.Wake.Words[slot].Recordings != 0 }
}

// Every read-only entity is either a setting a restore keeps from the device, or not a setting at all:
// a new one has to be placed in one or the other.
func TestEveryReadOnlyEntityIsKeptByARestore(t *testing.T) {
	for _, id := range readOnly {
		_, kept := keptByRestore[id]
		if kept == slices.Contains(notASetting, id) {
			t.Errorf("%s: list it once, in keptByRestore or notASetting", id)
		}
	}
	for id, k := range keptByRestore {
		if !slices.Contains(readOnly, id) {
			t.Errorf("%s is kept by a restore but not read-only here", id)
			continue
		}
		here := config.Defaults()
		here.Wake.Words = append(here.Wake.Words, config.WakeWord{ID: "word"}, config.WakeWord{ID: "word"})
		from := config.Defaults()
		from.Wake.Words = slices.Clone(here.Wake.Words)
		k.set(&from)
		file, err := config.Export(from, "", "v0")
		if err != nil {
			t.Fatal(err)
		}
		got, err := config.Restored(here, file, "")
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case k.on != nil && k.on(got.Config):
			t.Errorf("%s: a restored file turned it on", id)
		case k.on == nil:
			// A switch that is on by default: what the file says must not replace what the device has.
			if id == "sendspin" && got.Config.Sendspin != here.Sendspin {
				t.Errorf("sendspin: a restored file changed it")
			}
			if id == "mic_mute" && got.Config.Microphone.Muted != here.Microphone.Muted {
				t.Errorf("mic_mute: a restored file changed it")
			}
		}
	}
}
