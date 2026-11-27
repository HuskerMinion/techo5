//go:build !dot

package display

import (
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addonhost"
	"github.com/HuskerMinion/techo5/echod/internal/feature/addons"
	"github.com/HuskerMinion/techo5/echod/pkg/addonapi"
)

// The add-on host as the screen sees it (feature/addonhost): what the Show and the Spot read of add-ons'
// pages and cards, and tell of what the owner did with them.

// screenHost is the part of addonhost.Host the screen uses.
type screenHost interface {
	ScreenTiles() []addonhost.ScreenTile
	Scene(id, on string) (addonhost.Scene, bool)
	Shown(id, on string, shown bool)
	Touch(id, on, block string, rev uint64) bool
	Change(id, on, block string, rev uint64, value any) bool
	Left(id string)
	Swipe(id, direction string) bool
	Back(id string) bool
	DismissCard(id string)
	Asked(id string)
	Image(id string, b *addonapi.Block) ([]byte, string, bool)
}

// hostScene is an add-on's page or card as the host keeps it.
type hostScene = addonhost.Scene

// addonHost is the device's add-on host, or nil where add-ons do not run (a Panel); a variable for tests.
var addonHost = func() screenHost {
	if !addons.Here {
		return nil
	}
	return addonhost.Get()
}

// addonTilesFor is how long the list of add-ons with a page is kept before it is asked for again: it
// changes when one starts or stops, and asking costs a walk of the installed add-ons.
const addonTilesFor = 2 * time.Second

var tilesKept struct {
	mu    sync.Mutex
	at    time.Time
	tiles []addonhost.ScreenTile
}

// addonTiles is the running add-ons that have a page: the Add-ons tab's tiles, by name.
func addonTiles() []addonhost.ScreenTile {
	h := addonHost()
	if h == nil {
		return nil
	}
	tilesKept.mu.Lock()
	defer tilesKept.mu.Unlock()
	if tilesKept.at.IsZero() || time.Since(tilesKept.at) > addonTilesFor {
		tilesKept.tiles, tilesKept.at = h.ScreenTiles(), time.Now()
	}
	return tilesKept.tiles
}

// forgetTiles has the tiles asked for again at the next look: a test's add-ons changed.
func forgetTiles() {
	tilesKept.mu.Lock()
	tilesKept.at = time.Time{}
	tilesKept.mu.Unlock()
}

// addonName is a running add-on's name, as its listing gives it; its id when it has none.
func addonName(id string) string {
	for _, t := range addonTiles() {
		if t.ID == id {
			return t.Name
		}
	}
	return id
}

// addonIcon is a running add-on's icon, as its catalog entry gives it; "" for none, when its mark is its
// initials.
func addonIcon(id string) string {
	for _, t := range addonTiles() {
		if t.ID == id {
			return t.Icon
		}
	}
	return ""
}

// addonRunning is whether the add-on runs, with the screen granted.
func addonRunning(id string) bool {
	for _, t := range addonTiles() {
		if t.ID == id {
			return true
		}
	}
	return false
}
