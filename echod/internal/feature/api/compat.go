package api

// ESPHomeCompat is the ESPHome release this daemon tells Home Assistant it matches. Home Assistant
// reads the device info's version as ESPHome's, compares it with the release it considers current for
// Bluetooth proxies, and raises a repair ("update to take advantage of these improvements") below it.
// It goes in the device info's ESPHome version (Info.ESPHomeVersion) and in mDNS; the device info's
// Version is the daemon's own release, sent as the project's (api.go).
// The device does not run ESPHome, so there is nothing to update; reporting the release whose native
// API and proxy messages the daemon speaks keeps that notice away. Raise it when Home Assistant raises
// its bar and the daemon has been checked against that release's API.
const ESPHomeCompat = "2026.5.1"
