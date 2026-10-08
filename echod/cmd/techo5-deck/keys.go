package main

import (
	"fmt"
	"strings"
)

// A combination is written the way people write shortcuts: names joined by "+", modifiers first,
// "ctrl+shift+m", "alt+f4", "win+d", "media_play_pause". Case doesn't matter.

// modifier names, and the generic names of everything else that can be pressed.
var (
	modifiers = map[string]string{
		"ctrl": "ctrl", "control": "ctrl", "shift": "shift", "alt": "alt", "option": "alt",
		"win": "win", "windows": "win", "cmd": "win", "command": "win", "meta": "win", "super": "win",
	}
	namedKeys = map[string]bool{
		"enter": true, "return": true, "esc": true, "escape": true, "tab": true, "space": true,
		"backspace": true, "delete": true, "insert": true, "home": true, "end": true,
		"pageup": true, "pagedown": true, "up": true, "down": true, "left": true, "right": true,
		"printscreen": true, "minus": true, "equal": true, "comma": true, "period": true,
		"slash": true, "semicolon": true, "quote": true, "bracketleft": true, "bracketright": true,
		"backslash": true, "grave": true,
		"media_play_pause": true, "media_next": true, "media_previous": true, "media_stop": true,
		"volume_up": true, "volume_down": true, "volume_mute": true,
	}
	aliases = map[string]string{"return": "enter", "escape": "esc", "del": "delete", "pgup": "pageup", "pgdn": "pagedown",
		"-": "minus", "=": "equal", ",": "comma", ".": "period", "/": "slash", ";": "semicolon", "'": "quote",
		"[": "bracketleft", "]": "bracketright", "\\": "backslash", "`": "grave",
		"play_pause": "media_play_pause", "playpause": "media_play_pause", "next": "media_next",
		"previous": "media_previous", "prev": "media_previous", "mute": "volume_mute"}
)

// combo is a parsed combination: the modifiers held, then the key pressed.
type combo struct {
	mods []string // ctrl, shift, alt, win, in that order
	key  string   // "a".."z", "0".."9", "f1".."f24", or a namedKeys name; empty for modifiers alone
}

func (c combo) String() string {
	return strings.Join(append(append([]string(nil), c.mods...), c.key), "+")
}

// parseCombo reads a combination, refusing a name it doesn't know.
func parseCombo(s string) (combo, error) {
	var c combo
	held := map[string]bool{}
	low := strings.ToLower(strings.TrimSpace(s))
	// "ctrl++" is ctrl and the plus key, which is the = key: shortcuts that say "+" mean that key.
	if low == "+" || strings.HasSuffix(low, "++") {
		low = strings.TrimSuffix(low, "+") + "equal"
		low = strings.TrimPrefix(low, "+")
	}
	parts := strings.Split(low, "+")
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return combo{}, fmt.Errorf("%q has an empty part", s)
		}
		if m, ok := modifiers[p]; ok && i < len(parts)-1 {
			held[m] = true
			continue
		}
		if c.key != "" {
			return combo{}, fmt.Errorf("%q presses two keys; only modifiers can be held with one", s)
		}
		k, err := keyName(p)
		if err != nil {
			return combo{}, err
		}
		c.key = k
	}
	if c.key == "" && len(held) == 0 {
		return combo{}, fmt.Errorf("%q presses nothing", s)
	}
	for _, m := range []string{"ctrl", "shift", "alt", "win"} {
		if held[m] {
			c.mods = append(c.mods, m)
		}
	}
	return c, nil
}

func keyName(p string) (string, error) {
	if a, ok := aliases[p]; ok {
		p = a
	}
	if m, ok := modifiers[p]; ok {
		return m, nil // a modifier pressed on its own, like "win"
	}
	switch {
	case len(p) == 1 && (p[0] >= 'a' && p[0] <= 'z' || p[0] >= '0' && p[0] <= '9'):
		return p, nil
	case namedKeys[p]:
		return p, nil
	case len(p) >= 2 && p[0] == 'f':
		var n int
		if _, err := fmt.Sscanf(p[1:], "%d", &n); err == nil && n >= 1 && n <= 24 && fmt.Sprint(n) == p[1:] {
			return p, nil
		}
	}
	return "", fmt.Errorf("%q is not a key this agent knows", p)
}
