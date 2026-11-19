package buttons

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
)

// Every button Home Assistant is offered is one the board has, so none sits at unknown for good: on
// a Show or a Spot that leaves the action button out.
func TestOnlyTheButtonsTheBoardHasAreListed(t *testing.T) {
	for _, n := range order {
		if !buttons.Has(n) || buttons.Missing(n) {
			t.Errorf("the %s button is listed, though this board has none", n)
		}
	}
	for _, n := range []buttons.Name{buttons.Action, buttons.Mute, buttons.VolumeDown, buttons.VolumeUp, buttons.Power} {
		listed := false
		for _, o := range order {
			listed = listed || o == n
		}
		if buttons.Has(n) && !buttons.Missing(n) && !listed {
			t.Errorf("the %s button is not listed, though this board has it", n)
		}
	}
}
