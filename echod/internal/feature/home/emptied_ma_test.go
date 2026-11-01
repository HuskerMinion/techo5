package home

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
)

func holdTrack(t *testing.T, title string) {
	t.Helper()
	media.Get().ExternalTrack(title, "Toto", "Toto IV")
	media.Get().HoldRemote()
	t.Cleanup(media.Get().ForgetHeld)
	if _, _, _, ok := media.Get().Held(); !ok {
		t.Fatal("the track was not held")
	}
}

func fakeMA(t *testing.T, st hass.State, ok bool) {
	t.Helper()
	was := maState
	maState = func() (hass.State, bool) { return st, ok }
	t.Cleanup(func() { maState = was })
}

// clear_playlist leaves Music Assistant's entity stopped with no track named, and the held track goes (#150).
func TestAnEmptiedQueueLetsTheHeldTrackGo(t *testing.T) {
	holdTrack(t, "Africa")
	fakeMA(t, hass.State{State: "idle", Attributes: map[string]any{"media_content_type": "music"}}, true)
	checkEmptied("Africa")
	if _, _, _, ok := media.Get().Held(); ok {
		t.Fatal("an emptied queue's track is still held")
	}
}

// A pause from the Music Assistant app keeps its track named, and an entity off or unavailable, or Home
// Assistant out of reach, says nothing about the queue: the held track stays, with play on it.
func TestAPauseOrNoHomeAssistantKeepsTheHeldTrack(t *testing.T) {
	for name, c := range map[string]struct {
		st hass.State
		ok bool
	}{
		"paused":            {hass.State{State: "paused", Attributes: map[string]any{"media_title": "Africa"}}, true},
		"stopped and named": {hass.State{State: "idle", Attributes: map[string]any{"media_title": "Africa"}}, true},
		"no Home Assistant": {hass.State{}, false},
		"off":               {hass.State{State: "off", Attributes: map[string]any{"friendly_name": "Office"}}, true},
		"unavailable":       {hass.State{State: "unavailable"}, true},
	} {
		t.Run(name, func(t *testing.T) {
			holdTrack(t, "Africa")
			fakeMA(t, c.st, c.ok)
			checkEmptied("Africa")
			if _, _, _, ok := media.Get().Held(); !ok {
				t.Fatal("the held track went")
			}
		})
	}
}

// A different track held by the time the check runs is left alone.
func TestALaterHoldIsLeftAlone(t *testing.T) {
	holdTrack(t, "Rosanna")
	fakeMA(t, hass.State{State: "idle", Attributes: map[string]any{"media_content_type": "music"}}, true)
	checkEmptied("Africa")
	if _, _, _, ok := media.Get().Held(); !ok {
		t.Fatal("a later held track went")
	}
}
