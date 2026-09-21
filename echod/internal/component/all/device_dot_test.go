//go:build dot

package all

// notOnThisDevice is what the Echo Dot does not put up: it has no screen and no camera.
var notOnThisDevice = []string{
	// The announce word needs the openWakeWord front end, which this device cannot afford; see
	// detect/announce_available_dot.go.
	"announce_word_sensitivity",
	"camera_web_access", "screen", "screen_auto_brightness", "screen_clock_format", "screen_language",
	"screen_web_access",
	"slideshow_folder", "slideshow_interval",
	"slideshow_mode", "slideshow_screensaver_idle", "slideshow_screensaver_overlay",
	"slideshow_shuffle", "slideshow_subfolders",
}
