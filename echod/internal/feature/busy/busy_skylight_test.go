//go:build !dot && !spot && !panel

package busy

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// The Skylight updates its kernel as the 2nd gen Show 8 does (device.conf KERNEL_UPDATE), and its boot
// script waits on the marker the same way; a Show 5 has no kernel update and no marker.
func TestTheSkylightKeepsTheMarker(t *testing.T) {
	was := layout.Board
	t.Cleanup(func() { layout.Board = was })
	layout.Board = "skylight"
	if !keepsMarker() {
		t.Error("the Skylight keeps no marker")
	}
	layout.Board = "cronos"
	if keepsMarker() && !layout.AthenaMainline() {
		t.Error("a Show 5 keeps a marker")
	}
}
