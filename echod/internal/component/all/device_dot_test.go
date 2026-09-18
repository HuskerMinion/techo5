//go:build dot

package all

// notOnThisDevice is what the Echo Dot does not put up: it has no screen and no camera.
var notOnThisDevice = []string{
	"camera_web_access", "screen", "screen_24h_time", "screen_auto_brightness", "screen_web_access",
	"slideshow_mode", "slideshow_screensaver_idle", "slideshow_screensaver_overlay",
}
