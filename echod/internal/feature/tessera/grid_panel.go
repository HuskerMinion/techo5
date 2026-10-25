//go:build panel

package tessera

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// sizePath keeps the panel's screen size from its last start, so the grid the add-on is told at the
// next start is the screen's own before the screen has opened. A variable so a test keeps it somewhere
// harmless.
var sizePath = filepath.Join(layout.StateDir, "tessera-screen")

var (
	sizeMu    sync.Mutex
	sizeKnown grid // the screen's grid once it has said its size, zero until then
)

// screenGrid is the grid of the panel's screen: the size it said, the one it had last time, or a Show
// 8's until it has said one.
func screenGrid() grid {
	sizeMu.Lock()
	defer sizeMu.Unlock()
	if sizeKnown.width > 0 {
		return sizeKnown
	}
	var w, h int
	if b, err := os.ReadFile(sizePath); err == nil {
		if _, err := fmt.Sscanf(string(b), "%dx%d", &w, &h); err == nil && w > 0 && h > 0 {
			return gridForSize(w, h)
		}
	}
	return gridFor(true, false)
}

// SetPanelSize is the screen's size once it has opened: on a TECHO5 Panel the canvas the app asked for.
// It is kept for the next start, and with the Tessera screen on, a grid other than the one the add-on
// was told is told again: the Screen layout sensor and the tile sizes the next hello names.
func SetPanelSize(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	g := gridForSize(w, h)
	sizeMu.Lock()
	sizeKnown = g
	sizeMu.Unlock()
	if b, err := os.ReadFile(sizePath); err != nil || string(b) != fmt.Sprintf("%dx%d", w, h) {
		if err := os.WriteFile(sizePath, fmt.Appendf(nil, "%dx%d", w, h), 0o644); err != nil {
			slog.Warn("tessera: the screen size could not be kept", "err", err)
		}
	}
	t := Get()
	if t.enabled.Load() {
		t.showGrid(g, config.Get().Tessera.Grid)
	}
	t.regrid(chosen(g, config.Get().Tessera.Grid), "the grid follows the screen")
}
