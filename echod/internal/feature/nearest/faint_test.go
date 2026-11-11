package nearest

import (
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/announce"
)

// confirmBelow sets confirming and Confirm below nearest-device score for one test, and puts both back.
func confirmBelow(t *testing.T, confirm bool, below int) {
	t.Helper()
	set := func(c bool, b int) {
		if err := config.Set().OnHA().Confirm(c); err != nil {
			t.Fatal(err)
		}
		if err := config.Set().OnHA().ConfirmBelowScore(b); err != nil {
			t.Fatal(err)
		}
	}
	set(confirm, below)
	t.Cleanup(func() { set(false, 0) })
}

// A wake this device wins with a score under the setting is handed over to be asked about (#503), with
// when it was heard; one over it, or with the setting off or confirming off, opens as before.
func TestAFaintWinIsAskedAbout(t *testing.T) {
	cases := []struct {
		name    string
		confirm bool
		below   int
		score   float64
		slot    int
		asked   bool
	}{
		{"under the setting", true, 8, 3.7, 0, true},
		{"over it", true, 8, 14.3, 0, false},
		{"at it", true, 8, 8, 0, false},
		{"setting off", true, 0, 3.7, 0, false},
		{"confirming off", false, 8, 3.7, 0, false},
		{"the second wake word", true, 8, 3.7, 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHouse(t)
			confirmBelow(t, tc.confirm, tc.below)
			h.n.score = func() float64 { return tc.score }
			heard := h.now
			h.n.Woke(tc.slot)
			h.wait(300 * time.Millisecond)
			if tc.asked {
				if len(h.faint) != 1 || len(h.opened) != 0 || !h.faint[0].Equal(heard) {
					t.Fatalf("asked %v, opened %v; want asked once from %v", h.faint, h.opened, heard)
				}
				return
			}
			if len(h.faint) != 0 || len(h.opened) != 1 {
				t.Fatalf("asked %v, opened %v; want opened once", h.faint, h.opened)
			}
		})
	}
}

// The offset counts, as it does against the other devices: the score asked about is the one claimed.
func TestAFaintWinCountsTheOffset(t *testing.T) {
	h := newHouse(t)
	confirmBelow(t, true, 8)
	if err := config.Set().Nearest().Offset(6); err != nil {
		t.Fatal(err)
	}
	h.n.score = func() float64 { return 4 }
	h.n.Woke(0)
	h.wait(300 * time.Millisecond)
	if len(h.faint) != 0 || len(h.opened) != 1 {
		t.Fatalf("4 + 6 dB against 8: asked %v, opened %v", h.faint, h.opened)
	}
}

// Losing to a device that heard it better is unchanged: neither asked about nor opened.
func TestAFaintLossIsNotAskedAbout(t *testing.T) {
	h := newHouse(t)
	confirmBelow(t, true, 8)
	h.n.score = func() float64 { return 3 }
	h.n.Woke(0)
	h.claimFrom("Kitchen", 20)
	h.wait(300 * time.Millisecond)
	if len(h.faint) != 0 || len(h.opened) != 0 {
		t.Fatalf("beaten: asked %v, opened %v", h.faint, h.opened)
	}
}

// With nobody to compare with there is no score, and the wake opens at once as it always has.
func TestAFaintWakeWithNobodyToAskOpensAtOnce(t *testing.T) {
	h := newHouse(t)
	confirmBelow(t, true, 8)
	h.n.score = func() float64 { return 1 }
	h.n.peers = func() []announce.Peer { return nil }
	h.n.Woke(0)
	if h.at0 != 1 || len(h.faint) != 0 {
		t.Fatalf("alone: at once %d, asked %v", h.at0, h.faint)
	}
}
