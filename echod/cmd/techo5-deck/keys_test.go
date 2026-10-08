package main

import "testing"

func TestParseCombo(t *testing.T) {
	for in, want := range map[string]string{
		"ctrl+shift+m":     "ctrl+shift+m",
		"Shift + Ctrl + M": "ctrl+shift+m",
		"alt+F4":           "alt+f4",
		"win+d":            "win+d",
		"cmd+space":        "win+space",
		"media_play_pause": "media_play_pause",
		"playpause":        "media_play_pause",
		"ctrl+=":           "ctrl+equal",
		"ctrl++":           "ctrl+equal",
		"f13":              "f13",
		"win":              "win",
		"Return":           "enter",
		"ctrl+shift":       "ctrl+shift",
		"+":                "equal",
	} {
		c, err := parseCombo(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if got := c.String(); got != want {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "ctrl+", "a+b", "ctrl+f25", "f0", "hyper+x", "f01", "ctrl+a+"} {
		if c, err := parseCombo(bad); err == nil {
			t.Errorf("%q accepted as %q", bad, c)
		}
	}
}
