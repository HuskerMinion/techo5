package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLinuxKeys(t *testing.T) {
	for name, want := range map[string]uint16{"a": 30, "q": 16, "z": 44, "m": 50, "1": 2, "0": 11, "f1": 59, "f10": 68,
		"f11": 87, "f12": 88, "f13": 183, "f24": 194, "enter": 28, "win": 125, "media_play_pause": 164, "volume_mute": 113} {
		if got, err := linuxKey(name); err != nil || got != want {
			t.Errorf("%s = %d, %v; want %d", name, got, err, want)
		}
	}
	// Every name the agent accepts has a Linux key.
	for _, name := range []string{"esc", "tab", "space", "backspace", "delete", "insert", "home", "end", "pageup",
		"pagedown", "up", "down", "left", "right", "printscreen", "minus", "equal", "comma", "period", "slash",
		"semicolon", "quote", "bracketleft", "bracketright", "backslash", "grave", "media_next", "media_previous",
		"media_stop", "volume_up", "volume_down", "ctrl", "shift", "alt"} {
		if _, err := linuxKey(name); err != nil {
			t.Error(err)
		}
	}
	for r, want := range map[rune]struct {
		key   string
		shift bool
	}{'a': {"a", false}, 'A': {"a", true}, '!': {"1", true}, ' ': {"space", false}, '"': {"quote", true}, '\n': {"enter", false}} {
		k, s, err := usKeystroke(r)
		if err != nil || k != want.key || s != want.shift {
			t.Errorf("%q = %q %v %v", r, k, s, err)
		}
	}
	if _, _, err := usKeystroke('é'); err == nil {
		t.Error("é typed on Linux")
	}
}

func TestMacScripts(t *testing.T) {
	c, _ := parseCombo("cmd+shift+4")
	lang, s, err := macKeyScript(c)
	if err != nil || lang != "AppleScript" || s != `tell application "System Events" to key code 21 using {shift down, command down}` {
		t.Errorf("%s %q %v", lang, s, err)
	}
	c, _ = parseCombo("media_play_pause")
	if lang, s, err := macKeyScript(c); err != nil || lang != "JavaScript" || !strings.Contains(s, "(16 << 16)") {
		t.Errorf("media: %s %v", lang, err)
	}
	for _, bad := range []string{"win", "printscreen", "f24", "ctrl+volume_up"} {
		c, err := parseCombo(bad)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := macKeyScript(c); err == nil {
			t.Errorf("%s pressed on macOS", bad)
		}
	}
	// Quotes of every kind, backslashes, and the characters JavaScript would end a line at all stay
	// inside the JSON string.
	text := `Say "hi" “curly” «guillemets» back\slash ` + "\u2028\u2029" + `"); se.doShellScript("x` + "\nline 2"
	got, err := macTypeScript(text)
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(got, "var lines = ") + len("var lines = ")
	end := strings.Index(got[start:], ";\n") + start
	var lines []string
	if err := json.Unmarshal([]byte(got[start:end]), &lines); err != nil {
		t.Fatalf("the lines aren't one JSON value: %v\n%s", err, got)
	}
	if strings.Join(lines, "\n") != text {
		t.Errorf("text came back as %q", strings.Join(lines, "\n"))
	}
	if strings.ContainsAny(got[start:end], "\u2028\u2029") {
		t.Error("line separators left raw in the script")
	}
	c, _ = parseCombo("media_stop")
	if _, _, err := macKeyScript(c); err == nil {
		t.Error("media_stop pressed play/pause on macOS")
	}
}
