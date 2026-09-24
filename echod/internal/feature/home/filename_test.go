package home

import "testing"

// Home Assistant's own sound files are named for the file; streams are left to the station lists.
func TestFileName(t *testing.T) {
	for url, want := range map[string]string{
		"http://ha.example:8123/local/sleep/rain_deep.mp3":          "Rain deep",
		"http://ha.example:8123/local/sleep/heavy-thunderstorm.ogg": "Heavy thunderstorm",
		"http://ha.example:8123/media/local/fan.flac?authSig=x":     "Fan",
		"http://stream.example/live.mp3":                            "",
		"http://ha.example:8090/play?url=http%3A%2F%2Frain.example": "",
		"http://ha.example:8123/local/sleep/":                       "",
		"http://ha.example:8123/local/sleep/%C3%A9t%C3%A9_rain.mp3": "Été rain",
		"http://ha.example:8123/local/sleep/soft%20rain.mp3":        "Soft rain",
	} {
		if got := fileName(url); got != want {
			t.Errorf("fileName(%q) = %q, want %q", url, got, want)
		}
	}
}
