package main

// Linux input key codes (linux/input-event-codes.h) for the generic names (keys.go), and how plain
// text is typed on a US keyboard layout: the key, and whether shift is held. Kept free of build tags
// so the tables are tested everywhere; input_linux.go sends them.

import (
	"errors"
	"fmt"
)

var linuxKeys = map[string]uint16{
	"esc": 1, "minus": 12, "equal": 13, "backspace": 14, "tab": 15, "bracketleft": 26, "bracketright": 27,
	"enter": 28, "ctrl": 29, "semicolon": 39, "quote": 40, "grave": 41, "shift": 42, "backslash": 43,
	"comma": 51, "period": 52, "slash": 53, "alt": 56, "space": 57, "printscreen": 99,
	"home": 102, "up": 103, "pageup": 104, "left": 105, "right": 106, "end": 107, "down": 108,
	"pagedown": 109, "insert": 110, "delete": 111, "volume_mute": 113, "volume_down": 114,
	"volume_up": 115, "win": 125, "media_next": 163, "media_play_pause": 164, "media_previous": 165,
	"media_stop": 166,
}

// linuxKey is the key code for a generic name.
func linuxKey(name string) (uint16, error) {
	if c, ok := linuxKeys[name]; ok {
		return c, nil
	}
	if len(name) == 1 {
		c := name[0]
		switch {
		case c >= '1' && c <= '9':
			return uint16(2 + c - '1'), nil
		case c == '0':
			return 11, nil
		case c >= 'a' && c <= 'z':
			rows := []struct {
				keys  string
				first uint16
			}{{"qwertyuiop", 16}, {"asdfghjkl", 30}, {"zxcvbnm", 44}}
			for _, r := range rows {
				for i := range len(r.keys) {
					if r.keys[i] == c {
						return r.first + uint16(i), nil
					}
				}
			}
		}
	}
	var n int
	if _, err := fmt.Sscanf(name, "f%d", &n); err == nil {
		switch {
		case n >= 1 && n <= 10:
			return uint16(58 + n), nil
		case n == 11:
			return 87, nil
		case n == 12:
			return 88, nil
		case n >= 13 && n <= 24:
			return uint16(183 + n - 13), nil
		}
	}
	return 0, fmt.Errorf("no key %q on Linux", name)
}

// shifted is what each shifted character on a US keyboard is the shifted form of.
var shifted = map[rune]string{
	'!': "1", '@': "2", '#': "3", '$': "4", '%': "5", '^': "6", '&': "7", '*': "8", '(': "9", ')': "0",
	'_': "minus", '+': "equal", '{': "bracketleft", '}': "bracketright", '|': "backslash",
	':': "semicolon", '"': "quote", '~': "grave", '<': "comma", '>': "period", '?': "slash",
}

var plain = map[rune]string{
	'-': "minus", '=': "equal", '[': "bracketleft", ']': "bracketright", '\\': "backslash",
	';': "semicolon", '\'': "quote", '`': "grave", ',': "comma", '.': "period", '/': "slash",
	' ': "space", '\n': "enter", '\t': "tab",
}

// usKeystroke is how r is typed on a US layout.
func usKeystroke(r rune) (key string, shift bool, err error) {
	switch {
	case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		return string(r), false, nil
	case r >= 'A' && r <= 'Z':
		return string(r - 'A' + 'a'), true, nil
	}
	if k, ok := plain[r]; ok {
		return k, false, nil
	}
	if k, ok := shifted[r]; ok {
		return k, true, nil
	}
	return "", false, errors.New("the text has a character that can't be typed on Linux yet: plain letters, digits and punctuation only")
}
