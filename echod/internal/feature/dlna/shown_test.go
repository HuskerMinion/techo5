package dlna

import "testing"

// The controller that set a stream gets its address whole; any other device gets it with credential-
// like query values blanked, so a Plex server's token is not handed to whoever asks what is playing.
func TestTheStreamsAddressHidesItsTokenFromOthers(t *testing.T) {
	s := song{uri: "http://192.168.1.20:32400/video/start?offset=60&X-Plex-Token=abc123&session=s1", from: "127.0.0.1"}
	if got := shownURI(s, "127.0.0.1"); got != s.uri {
		t.Errorf("to its own controller: %q", got)
	}
	if got := shownURI(s, "::ffff:127.0.0.1"); got != s.uri {
		t.Errorf("to its own controller, IPv6-mapped: %q", got)
	}
	want := "http://192.168.1.20:32400/video/start?offset=60&X-Plex-Token=&session=s1"
	if got := shownURI(s, "192.168.1.30"); got != want {
		t.Errorf("to another device: %q, want %q", got, want)
	}
	for in, out := range map[string]string{
		"http://u:pw@h/a?api_key=1&k=2":   "http://h/a?api_key=&k=2",
		"http://h/a?access_token=x&key=y": "http://h/a?access_token=&key=",
		"http://h/a":                      "http://h/a",
		"%zz":                             "",
	} {
		if got := shownURI(song{uri: in, from: "10.0.0.1"}, "10.0.0.2"); got != out {
			t.Errorf("%q: %q, want %q", in, got, out)
		}
	}
	if got := shownURI(song{}, "10.0.0.2"); got != "" {
		t.Errorf("nothing playing: %q", got)
	}
}
