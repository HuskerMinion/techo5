package setup

import "testing"

// An address with a login is never kept, with a scheme or without one; an address without one is.
func TestALoginIsFoundWithOrWithoutAScheme(t *testing.T) {
	for v, want := range map[string]bool{
		"rtsp://viewer:secret@192.168.1.20:554/s": true,
		"viewer:secret@192.168.1.20:1984":         true,
		"http://192.168.1.20:1984":                false,
		"someone@example.com":                     false,
		"192.168.1.20":                            false,
	} {
		if got := hasLogin([]string{v}); got != want {
			t.Errorf("hasLogin(%q) = %v, want %v", v, got, want)
		}
	}
}
