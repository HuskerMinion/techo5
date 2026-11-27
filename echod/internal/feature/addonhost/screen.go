package addonhost

import (
	"log/slog"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addons"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi/serve"
)

// The add-on screen (stage 2, "screen1"), the device's half: the display gives the host its side
// (SetScreen), is told when an add-on's page or card changes and when one asks for its page, reads the
// scene to draw it, and tells the host what the owner did. The scenes themselves are kept, checked and
// merged by the server (pkg/addonapi/serve), in RAM only, under the add-on whose uid they came from, and
// go with its last connection, when it stops and when it is removed. Nothing about them is written.

// ScreenSink is what the display implements: see serve.Screen. Each call says only that something may
// have changed, and must return at once.
type ScreenSink = serve.Screen

// Scene is an add-on's page or card as the display draws it; see serve.Scene.
type Scene = serve.Scene

// NopScreen draws nothing: for a build whose display has no add-on screen yet.
type NopScreen = serve.NopScreen

// hasScreen is whether this build's device has a screen an add-on could show on: a Dot has none, so
// its screen ops are refused (unavailable) whatever is set, and the catalog keeps a version with the
// screen capability from naming the Dot at all.
func hasScreen() bool { return addons.Family() != "dot" }

// SetScreen gives the host the display's side of the add-on screen, which the display does as it
// starts; until it has, and on a device without a screen, the screen's ops answer unavailable.
func (h *Host) SetScreen(sc ScreenSink) {
	if sc != nil && !hasScreen() {
		slog.Warn("addonhost: a screen was given to a device without one; add-ons are refused it")
		sc = nil
	}
	h.srv.SetScreen(sc)
}

// ScreenTile is a tile under the Add-ons tab: a running add-on granted the screen, by its id and the
// name its listing gives it, which the page's name bar shows too.
type ScreenTile struct {
	ID   string
	Name string
}

// ScreenTiles is the tiles under the Add-ons tab, by id: there while the add-on runs, gone when it stops.
func (h *Host) ScreenTiles() []ScreenTile {
	names := h.names()
	var out []ScreenTile
	for _, id := range h.srv.Screens() {
		name := names[id]
		if name == "" {
			name = id
		}
		out = append(out, ScreenTile{ID: id, Name: name})
	}
	return out
}

// Scene is the add-on's page or card (addonapi.OnPage, addonapi.OnCard) to draw, and whether it has
// one.
func (h *Host) Scene(id, on string) (Scene, bool) { return h.srv.Scene(id, on) }

// Shown tells the add-on its page or card came into view or went out of it. The display marks it
// hidden whenever a screen the device reserves (the PIN pad, the settings, a question, a ring, a call,
// the camera, first-run) covers it, so no touch reaches an add-on then.
func (h *Host) Shown(id, on string, shown bool) { h.srv.Shown(id, on, shown) }

// Touch is a tap on one of the add-on's buttons or chips in view, by its id: false when it is not one.
func (h *Host) Touch(id, on, block string) bool { return h.srv.Touch(id, on, block) }

// Swipe is a swipe left or right (addonapi.SwipeLeft, SwipeRight) across the add-on's page in view.
func (h *Host) Swipe(id, direction string) bool { return h.srv.Swipe(id, direction) }

// Back is the back gesture on the add-on's page in view.
func (h *Host) Back(id string) bool { return h.srv.Back(id) }

// DismissCard is the owner swiping the add-on's card away.
func (h *Host) DismissCard(id string) { h.srv.DismissCard(id) }
