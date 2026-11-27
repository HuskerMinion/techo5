package addonhost

import (
	"log/slog"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addons"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
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

// ScreenTile is a tile under the Add-ons tab: a running add-on granted the screen, by its id, the name
// its listing gives it, which the page's name bar shows too, and the icon its catalog entry gives it
// (catalog.Version.Icon), its mark beside the name; "" for none, when the mark is its initials. The icon
// is the catalog reviewer's: nothing the add-on sends sets or changes it.
type ScreenTile struct {
	ID   string
	Name string
	Icon string
}

// ScreenTiles is the tiles under the Add-ons tab, by id: there while the add-on runs, gone when it stops.
func (h *Host) ScreenTiles() []ScreenTile {
	names, icons := h.names(), h.icons()
	var out []ScreenTile
	for _, id := range h.srv.Screens() {
		// The catalog's name, drawn on the screen: whatever mkcatalog let through, nothing in it that
		// hides or reorders the words around it.
		name := strings.TrimSpace(addonapi.StripHidden(names[id]))
		if name == "" {
			name = id
		}
		out = append(out, ScreenTile{ID: id, Name: name, Icon: icons[id]})
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

// Touch is a tap on one of the add-on's buttons or chips in view, by its id, as found in the layout of
// the scene's revision rev (Scene.Rev): false when it is not one, or when the block changed since.
func (h *Host) Touch(id, on, block string, rev uint64) bool { return h.srv.Touch(id, on, block, rev) }

// Swipe is a swipe left or right (addonapi.SwipeLeft, SwipeRight) across the add-on's page in view.
func (h *Host) Swipe(id, direction string) bool { return h.srv.Swipe(id, direction) }

// Back is the back gesture on the add-on's page in view.
func (h *Host) Back(id string) bool { return h.srv.Back(id) }

// Left is the owner putting the add-on's page away by the name bar's way out: the add-on may not bring
// it straight back on what an earlier tap allowed.
func (h *Host) Left(id string) { h.srv.Left(id) }

// DismissCard is the owner swiping the add-on's card away.
func (h *Host) DismissCard(id string) { h.srv.DismissCard(id) }

// Asked is a tap on the add-on's tile or its card: a person asking something of it, after which it may
// bring its page up for a moment (screen.open). Its commands and the taps on its blocks count already.
func (h *Host) Asked(id string) { h.srv.Asked(id) }

// Image is the bytes an image block of the add-on's shows and a key naming them, for the display to
// decode and scale off its draw path; false while there are none yet (an address being fetched, a name
// not put). See serve.Server.Image.
func (h *Host) Image(id string, b *addonapi.Block) ([]byte, string, bool) { return h.srv.Image(id, b) }
