//go:build !dot && !spot

package display

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Calendar chips is Normal until somebody picks Small, and Small draws the chips on their own rows at
// the header line's size: the same face, height, padding and gap.
func TestCalendarChipSize(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	r := testRenderer()
	_, lineH := r.chipFace(true)
	_, rowH := r.chipFace(false)
	if rowH <= lineH || r.chipPad(false) <= r.chipPad(true) {
		t.Fatalf("by default a chip on its own row is the larger one: %d over %d", rowH, lineH)
	}

	s, ok := lookByRow(lookRowPrefix + "chip_size")
	if !ok {
		t.Fatal("no Calendar chips row")
	}
	if !slices.Equal(s.opts, []string{"Normal", "Small"}) || lookOpt(s, deviceLook()) != 0 {
		t.Fatalf("Calendar chips offers %v and starts on %d", s.opts, lookOpt(s, deviceLook()))
	}
	if err := s.set(false); err != nil {
		t.Fatal(err)
	}
	if !config.Get().Calendar.SmallChips || lookOpt(s, deviceLook()) != 1 {
		t.Fatal("choosing Small is saved and shown")
	}
	f, h := r.chipFace(false)
	lf, lh := r.chipFace(true)
	if f != lf || h != lh || r.chipPad(false) != r.chipPad(true) || r.chipGap(false) != r.chipGap(true) {
		t.Errorf("small chips on their own rows are the header line's: height %d, want %d", h, lh)
	}
	if err := s.set(true); err != nil {
		t.Fatal(err)
	}
	if _, h := r.chipFace(false); h != rowH {
		t.Errorf("Normal again draws them at %d, want %d", h, rowH)
	}
}
