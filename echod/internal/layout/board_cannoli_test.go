//go:build dot && cannoli

package layout

import "testing"

// The Echo Dot 5th gen's build runs as it on any unit: none of the other Dots can run it.
func TestTheDot5BuildIsTheDot5(t *testing.T) {
	if Board != boardCannoli || Model != "Echo Dot 5 (cannoli)" || DefaultName != "Echo Dot" {
		t.Fatalf("board %q, model %q, name %q", Board, Model, DefaultName)
	}
	if !Cannoli() || Donut() || Cupcake() || Radar() {
		t.Errorf("Cannoli() %v, Donut() %v, Cupcake() %v, Radar() %v", Cannoli(), Donut(), Cupcake(), Radar())
	}
}
