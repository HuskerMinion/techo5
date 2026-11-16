package redact

import "testing"

// An address keeps its scheme, host and path, and loses its login, query and fragment; the words
// around it, and a question mark that is not in an address, stay.
func TestURLSecrets(t *testing.T) {
	for in, want := range map[string]string{
		`Get "http://cam.local/snap.jpg?token=abc": dial tcp: refused`: `Get "http://cam.local/snap.jpg": dial tcp: refused`,
		"rtsp://admin:hunter2@cam.local:554/stream1 failed":            "rtsp://cam.local:554/stream1 failed",
		"https://u@h.example/p#frag?x=1":                               "https://h.example/p",
		"no picture? try again":                                        "no picture? try again",
		"https://h.example/a?b=c@d:":                                   "https://h.example/a:",
		// A password holding / ? # or a space leaves none of itself behind.
		"rtsp://admin:pa/ss@cam.local:554/s":   "rtsp://cam.local:554/s",
		"rtsp://admin:ab?cd@cam.local:554/s":   "rtsp://cam.local:554/s",
		"http://admin:a#b@host.local:1984/api": "http://host.local:1984/api",
		"http://cam.local:8080/x@y":            "http://cam.local:8080/x@y",
		"rtsp://[fe80::1]:554/s":               "rtsp://[fe80::1]:554/s",
	} {
		if got := URLSecrets(in); got != want {
			t.Errorf("URLSecrets(%q) = %q, want %q", in, got, want)
		}
	}
}
