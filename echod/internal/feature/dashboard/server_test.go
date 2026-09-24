//go:build !dot

package dashboard

import "testing"

// The dashcast address as people type it. "https://…:9555" from a browser's address bar was kept
// as it was and could never be dialed (techo5#26).
func TestNormalizeServer(t *testing.T) {
	for in, want := range map[string]string{
		"":                                  "",
		"   ":                               "",
		"192.168.1.20:9555":                 "192.168.1.20:9555",
		"192.168.1.20":                      "192.168.1.20:9555",
		" dashcast.example.lan ":            "dashcast.example.lan:9555",
		"https://dashcast.example.lan:9555": "dashcast.example.lan:9555",
		"http://dashcast.example.lan:9555/": "dashcast.example.lan:9555",
		"https://dashcast.example.lan/some/path?x=1": "dashcast.example.lan:9555",
		"tcp://192.168.1.20:7000":                    "192.168.1.20:7000",
		"user@192.168.1.20:9555":                     "192.168.1.20:9555",
		"[fd00::20]:9555":                            "[fd00::20]:9555",
		"[fd00::20]":                                 "[fd00::20]:9555",
		"fd00::20":                                   "[fd00::20]:9555",
	} {
		got, err := NormalizeServer(in)
		if err != nil || got != want {
			t.Errorf("NormalizeServer(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"dash cast:9555", "host:port", "host:0", "host:70000", "-host:9555", "a:b:c:d:e:g:9555", "https://"} {
		if got, err := NormalizeServer(bad); err == nil {
			t.Errorf("NormalizeServer(%q) = %q, want an error", bad, got)
		}
	}
}
