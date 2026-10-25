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
// radio, so there is no proxy (bluetooth.Here) and no Bluetooth audio (btaudio stands down on
// Android); and the tablet's camera is Android's, so there is no camera page (security's cameraPage).
var notOnPanel = []string{
	"update_channel", "airplay", "spotify_connect",
	"video", "dlna_video", "video_state", "video_title", "video_error", "camera_live", "camera_sound",
	"bluetooth_proxy", "bluetooth_proxy_connections", "ble_advertisements",
	"bluetooth_audio", "bluetooth_disconnect", "bluetooth_forget", "bluetooth_forget_phones", "bluetooth_pairing",
	"bluetooth_phone_pairing", "bluetooth_reconnect", "bluetooth_reconnect_phone", "bluetooth_secure_connections",
	"bluetooth_sets_volume", "camera_web_access", "screen_night_warmth",
	// A tablet's keys are Android's: the app hands the daemon no buttons (hardware/buttons.Here).
	"button_action", "button_mute", "button_volume_down", "button_volume_up",
	// The hotplug governor is the kernel's, which an app may not write (diag's coresHere).
	"min_cores",
}

// onlyOnPanel is what a panel adds: Android can silence a tablet's microphone under the app.
var onlyOnPanel = []string{"microphone_silent"}
