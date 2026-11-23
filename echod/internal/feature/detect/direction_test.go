package detect

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
)

// Wake word direction exists exactly where the array was measured, and nowhere else: a board that
// cannot tell must not grow an entity that never says anything.
func TestDirectionOnlyWhereTheArrayWasMeasured(t *testing.T) {
	if got := newDirectionEntity() != nil; got != mic.HasDirection() {
		t.Errorf("direction entity offered: %v, array measured: %v", got, mic.HasDirection())
	}
	// Nothing to set on a board without one, and nothing breaks for asking.
	setDirection(nil)
}
