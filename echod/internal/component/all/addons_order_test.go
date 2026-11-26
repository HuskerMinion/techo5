//go:build !panel

package all

import (
	"slices"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/component"
)

// The add-ons' update entities are read before the add-ons' own entities: on a key they share, the
// add-on's entity is the one left out, never the update entity.
func TestUpdateEntitiesComeBeforeAddonEntities(t *testing.T) {
	var names []string
	for _, c := range component.Default().All() {
		names = append(names, c.Name())
	}
	addons, host := slices.Index(names, "addons"), slices.Index(names, "add-on API")
	if addons < 0 || host < 0 || addons > host {
		t.Errorf("addons at %d, the add-on API at %d", addons, host)
	}
}
