package main

// macOS presses keys through osascript, without anything compiled against Apple's libraries:
// AppleScript's System Events for keys, and short JavaScript for Automation scripts for text and for
// the media keys, which System Events can't press. Kept free of build tags so what they send is
// tested everywhere; input_darwin.go runs them.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// macKeyCodes are macOS's virtual key codes (the US layout's positions) for the generic names.
var macKeyCodes = map[string]int{
	"a": 0, "s": 1, "d": 2, "f": 3, "h": 4, "g": 5, "z": 6, "x": 7, "c": 8, "v": 9, "b": 11, "q": 12,
	"w": 13, "e": 14, "r": 15, "y": 16, "t": 17, "1": 18, "2": 19, "3": 20, "4": 21, "6": 22, "5": 23,
	"equal": 24, "9": 25, "7": 26, "minus": 27, "8": 28, "0": 29, "bracketright": 30, "o": 31, "u": 32,
	"bracketleft": 33, "i": 34, "p": 35, "enter": 36, "l": 37, "j": 38, "quote": 39, "k": 40,
	"semicolon": 41, "backslash": 42, "comma": 43, "slash": 44, "n": 45, "m": 46, "period": 47,
	"tab": 48, "space": 49, "grave": 50, "backspace": 51, "esc": 53,
	"f1": 122, "f2": 120, "f3": 99, "f4": 118, "f5": 96, "f6": 97, "f7": 98, "f8": 100, "f9": 101,
	"f10": 109, "f11": 103, "f12": 111, "f13": 105, "f14": 107, "f15": 113, "f16": 106, "f17": 64,
	"f18": 79, "f19": 80, "f20": 90,
	"insert": 114, "home": 115, "pageup": 116, "delete": 117, "end": 119, "pagedown": 121,
	"left": 123, "right": 124, "down": 125, "up": 126,
}

// macMediaKeys are the system's own media key numbers (NX_KEYTYPE_*).
var macMediaKeys = map[string]int{
	"volume_up": 0, "volume_down": 1, "volume_mute": 7, "media_play_pause": 16, "media_next": 17,
	"media_previous": 18,
}

var macModifiers = map[string]string{"ctrl": "control down", "shift": "shift down", "alt": "option down", "win": "command down"}

// macKeyScript is the osascript arguments that press c: the language ("AppleScript" or
// "JavaScript") and the script.
func macKeyScript(c combo) (lang, script string, err error) {
	if c.key == "media_stop" {
		return "", "", fmt.Errorf("macOS has no media stop key; use media_play_pause")
	}
	if n, ok := macMediaKeys[c.key]; ok {
		if len(c.mods) > 0 {
			return "", "", fmt.Errorf("%s can't be held with a media key", strings.Join(c.mods, "+"))
		}
		return "JavaScript", macMediaScript(n), nil
	}
	code, ok := macKeyCodes[c.key]
	if !ok {
		if _, mod := macModifiers[c.key]; mod {
			return "", "", fmt.Errorf("a modifier on its own can't be pressed on macOS")
		}
		return "", "", fmt.Errorf("no key %q on macOS", c.key)
	}
	script = fmt.Sprintf(`tell application "System Events" to key code %d`, code)
	if len(c.mods) > 0 {
		var using []string
		for _, m := range c.mods {
			using = append(using, macModifiers[m])
		}
		script += " using {" + strings.Join(using, ", ") + "}"
	}
	return "AppleScript", script, nil
}

// macMediaScript presses and releases a media key the way the keyboard's own media keys do: a
// system-defined event, subtype 8, the key in the top half of data1 and down (0xa) or up (0xb) in
// the next byte.
func macMediaScript(key int) string {
	return fmt.Sprintf(`ObjC.import("Cocoa");
function post(down) {
  var flags = down ? 0xa00 : 0xb00;
  var data = (%d << 16) | ((down ? 0xa : 0xb) << 8);
  var ev = $.NSEvent.otherEventWithTypeLocationModifierFlagsTimestampWindowNumberContextSubtypeData1Data2(
    14, $.NSMakePoint(0, 0), flags, 0, 0, null, 8, data, -1);
  $.CGEventPost(0, ev.CGEvent);
}
post(true); post(false);`, key)
}

// macTypeScript types s through JavaScript for Automation: the text is a JSON string in the script,
// which nothing in it can end early, so no text can turn into a command. Each line is typed as
// keystrokes, with Return between lines.
func macTypeScript(s string) (string, error) {
	lines, err := json.Marshal(strings.Split(strings.ReplaceAll(s, "\r", ""), "\n"))
	if err != nil {
		return "", err
	}
	return `var se = Application("System Events");
var lines = ` + string(lines) + `;
for (var i = 0; i < lines.length; i++) {
  if (i > 0) se.keyCode(36);
  if (lines[i] !== "") se.keystroke(lines[i]);
}`, nil
}
