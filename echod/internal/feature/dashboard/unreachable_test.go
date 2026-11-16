//go:build !dot

package dashboard

import (
	"strings"
	"testing"
)

// A Home Assistant that does not answer is said in words, with what to do if it moved, and not by its
// address: the screen is seen by everyone in the room, and the log has where it was looked for.
func TestUnreachableSaysWhatToDo(t *testing.T) {
	if got := unreachable("http://192.0.2.10:8123"); strings.Contains(got, "192.0.2.10") || !strings.Contains(got, "home_assistant action") {
		t.Errorf("unreachable = %q", got)
	}
	if got := unreachable(""); got != "Can't reach Home Assistant." {
		t.Errorf("unreachable with no address = %q", got)
	}
}

// A device with no token names the action that gives it one, as Home Assistant lists it (#551).
func TestNoAccessNamesTheAction(t *testing.T) {
	if got := noAccess("Living Room", ""); !strings.Contains(got, "esphome.living_room_home_assistant") || !strings.Contains(got, "url and token") {
		t.Errorf("noAccess = %q", got)
	}
}

// The server's sentence is shown without the error it ends with, which can name the page's address with
// a token in its query; a sentence with no error after it is shown as it is.
func TestTheServersProblemIsItsSentence(t *testing.T) {
	for in, want := range map[string]string{
		`The dashboard would not open: page load error net::ERR at "http://ha.local:8123/lovelace?auth_callback=1&code=secret"`: "The dashboard would not open.",
		"The dashboard server has no web page called Kitchen.":                                                                  "The dashboard server has no web page called Kitchen.",
	} {
		if got := serverProblem(in); got != want {
			t.Errorf("serverProblem(%q) = %q, want %q", in, got, want)
		}
	}
}
