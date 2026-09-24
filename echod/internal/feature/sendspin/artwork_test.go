package sendspin

import "testing"

// Which messages are the player's. Music Assistant ends its artwork stream with stream/end naming only
// "artwork", and read as the player's that stopped the music along with the picture. No roles at all
// is the spec's "every role".
func TestNamesRoles(t *testing.T) {
	for _, tc := range []struct {
		name  string
		roles []string
		role  string
		want  bool
	}{
		{"no roles is every role: player", nil, "player", true},
		{"no roles is every role: artwork", nil, "artwork", true},
		{"the player's end", []string{"player"}, "player", true},
		{"the player's end is not the picture's", []string{"player"}, "artwork", false},
		{"the picture's end is not the player's", []string{"artwork"}, "player", false},
		{"the picture's end", []string{"artwork"}, "artwork", true},
		{"a visualizer's clear is not the player's", []string{"visualizer"}, "player", false},
		{"both named", []string{"artwork", "player"}, "player", true},
	} {
		if got := names(tc.roles, tc.role); got != tc.want {
			t.Errorf("%s: names(%v, %q) = %v, want %v", tc.name, tc.roles, tc.role, got, tc.want)
		}
	}
}
