//go:build !dot && !spot

package privacy

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// On the 2nd gen the button has always acted by the time the press is heard; on the 1st gen only a
// press that releases the latch has, and one with the microphones live is the daemon's to act on.
func TestHardwareActs(t *testing.T) {
	was := layout.Board
	t.Cleanup(func() { layout.Board = was })

	layout.Board = "cronos"
	if !(platform{}).HardwareActs(false) || !(platform{}).HardwareActs(true) {
		t.Error("2nd gen: the button toggles the latch itself, both ways")
	}
	layout.Board = "checkers"
	if (platform{}).HardwareActs(false) {
		t.Error("1st gen, microphones live: the press must be acted on, or it never mutes")
	}
	if !(platform{}).HardwareActs(true) {
		t.Error("1st gen, muted: the button has released the latch; acting would mute again")
	}
}

// Seeding does nothing where the hardware holds the microphones. It matters that this stays a no-op:
// unmuting a Show writes the latch and never touches this flag, so a Show that seeded it true would
// keep handing on silence after the owner unmuted, with the hardware saying the microphones are live.
func TestSeedingDoesNothingOnABoardWhoseHardwareCuts(t *testing.T) {
	Seed(true)
	if SoftwareCut() {
		t.Error("a Show seeded the software cut, which nothing on a Show ever clears")
	}
}
