package detect

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
)

// Wake word direction exists exactly where the array was measured, and nowhere else: a board that
// cannot tell must not grow an entity that never says anything.
func TestDirectionOnlyWhereTheArrayWasMeasured(t *testing.T) {
	d := newDirection()
	if got := d != nil; got != mic.HasDirection() {
		t.Errorf("direction entity offered: %v, array measured: %v", got, mic.HasDirection())
	}
	if d != nil && (len(d.entities()) != 2 || d.send.ObjectID != "send_wake_direction") {
		t.Errorf("entities %v, want the sensor and Send wake word direction", d.entities())
	}
	// Nothing to set on a board without one, and nothing breaks for asking.
	var none *direction
	none.set()
	if none.entities() != nil {
		t.Error("a board without a direction lists entities for it")
	}
}
