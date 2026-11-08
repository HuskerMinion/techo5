//go:build !dot

package dashboard

import (
	"encoding/json"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// A card that navigates to the library's hash keeps its media player with the tap, for the library to
// play on; any other navigate is a view as ever. The dashboard goes nowhere on a hash: on a screen
// without the library it is not a view of that name.
func TestALibraryCardKeepsItsPlayer(t *testing.T) {
	for _, c := range []struct {
		card, entity, view, want string
	}{
		{`{"tap_action": {"action": "navigate", "navigation_path": "#techo5-library"}}`, "media_player.kitchen", LibraryPath, "media_player.kitchen"},
		{`{"tap_action": {"action": "navigate", "navigation_path": "#techo5-library"}}`, "", LibraryPath, ""},
		{`{"tap_action": {"action": "navigate", "navigation_path": "/home/music"}}`, "media_player.kitchen", "home/music", ""},
	} {
		var card raw
		if err := json.Unmarshal([]byte(c.card), &card); err != nil {
			t.Fatal(err)
		}
		a := actionOf(card, c.entity)
		if a == nil || a.View != c.view || a.Entity != c.want || a.Service != "" {
			t.Errorf("%s with %q: %+v", c.card, c.entity, a)
		}
	}

	f := drawnAt(t, "home/room")
	f.Tap(Action{View: LibraryPath, Entity: "media_player.kitchen"})
	if p := config.Get().Dashboard.Path; p != "home/room" {
		t.Errorf("the library's hash went to %q", p)
	}
}
