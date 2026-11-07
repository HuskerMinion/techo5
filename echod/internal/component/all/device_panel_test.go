//go:build panel

package all

import "github.com/HuskerMinion/techo5/echod/internal/lib/panelscreen"

// No app answers in a test: the screen gives up on it at once rather than waiting for it at every
// restore of every setting (panelscreen.DialWait), which took this package past its ten minutes.
func init() { panelscreen.DialWait = 0 }

// notOnPanel is what a TECHO5 Panel leaves out of the Show's list, as a tablet has it (layout.OnAndroid
// is true in a panel build, tests too): the app updates itself, so there is no channel to follow; an
// app has no AirPlay or Spotify Connect programs to run (streaming.Here) and no techo5-ffmpeg, so no
// video player (video.Here) and no camera stream (home's cameraStreamHere); Android has the Bluetooth
// radio and lends BLE reports/one GATT slot; Bluetooth audio stays in Android (btaudio stands down on
// Android); Android's radio is not BlueZ, so the wall clock's RFCOMM bridge is not there either
// (wallclock.Here); and the tablet's camera is Android's, so there is no camera page (security's cameraPage).
var notOnPanel = []string{
	"update_channel", "airplay", "airplay_sets_volume", "spotify_connect",
	"video", "dlna_video", "video_state", "video_title", "video_error", "camera_live", "camera_sound",
	"bluetooth_audio", "bluetooth_disconnect", "bluetooth_forget", "bluetooth_forget_phones", "bluetooth_pairing",
	"bluetooth_phone_pairing", "bluetooth_reconnect", "bluetooth_reconnect_phone", "bluetooth_secure_connections",
	"bluetooth_sets_volume", "camera_web_access", "screen_night_warmth",
	// The wall clock bridge needs BlueZ over BR/EDR (wallclock.Here is false on Android).
	"wall_clock", "wall_clock_pair", "wall_clock_status",
	// A tablet's keys are Android's: the app hands the daemon no buttons (hardware/buttons.Here).
	"button_action", "button_mute", "button_volume_down", "button_volume_up",
	// The hotplug governor is the kernel's, which an app may not write (diag's coresHere).
	"min_cores",
	// A tablet's Wi-Fi is Android's (wifiquiet).
	"wifi_prefer_5ghz", "wifi_quiet_hours", "wifi_quiet_hours_end", "wifi_quiet_hours_start",
}

// onlyOnPanel is what a panel adds: Android can silence a tablet's microphone under the app.
var onlyOnPanel = []string{"microphone_silent"}
