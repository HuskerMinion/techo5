package home

import (
	"log/slog"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

// emptiedAfter is how long after Music Assistant stops this player its own entity is asked what it
// is doing: long enough for Home Assistant to have heard it too.
const emptiedAfter = 5 * time.Second

// maState is Music Assistant's entity for this device as Home Assistant has it; a test puts its own in.
var maState = func() (hass.State, bool) {
	ma, err := musicAssistantPlayer()
	if err != nil || ma == "" {
		return hass.State{}, false
	}
	st, err := hass.Get().State(ma)
	return st, err == nil
}

// CheckEmptied looks, a little after Music Assistant stopped this player with nobody here asking, at
// whether it was a pause or its queue being emptied. Over Sendspin the two look the same: the stream
// ends and the track is cleared, so the track is held for the screen as after a pause. Home Assistant
// tells them apart: a pause leaves Music Assistant's entity paused with its track named, and an emptied
// queue (clear_playlist) leaves it with no media_title. Then the held track is let go at once, instead
// of after the ten minutes a paused strip stays (#150). Without Home Assistant nothing changes.
func CheckEmptied() {
	title, _, _, ok := media.Get().Held()
	if !ok {
		return
	}
	time.AfterFunc(emptiedAfter, func() { checkEmptied(title) })
}

// checkEmptied lets the held track go if it is still title and Music Assistant's queue is empty.
func checkEmptied(title string) {
	if held, _, _, ok := media.Get().Held(); !ok || held != title {
		return
	}
	st, ok := maState()
	if !ok || st.State == "paused" || st.State == "playing" || st.State == "buffering" {
		return
	}
	// Still a player with its media attributes, only no track: an entity off or unavailable has none of
	// them, and says nothing about the queue.
	if kind, _ := st.Attributes["media_content_type"].(string); kind == "" {
		return
	}
	if named, _ := st.Attributes["media_title"].(string); named != "" {
		return
	}
	slog.Info("music assistant: queue emptied, letting the held track go", "state", st.State)
	media.Get().ForgetHeld()
}
