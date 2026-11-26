package api

import (
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
)

// An entity whose key another already has is left out, and the rest are served: one add-on's name
// can never cost the device its link to Home Assistant.
func TestADuplicateEntityIsLeftOutNotFatal(t *testing.T) {
	ents := esphome.NewEntities()
	addEach(ents, []esphome.Entity{
		&esphome.Button{Base: esphome.Base{ObjectID: "addon_foo_bar_x"}},
		&esphome.Button{Base: esphome.Base{ObjectID: "addon_foo_bar_x"}},
		&esphome.Button{Base: esphome.Base{ObjectID: "addon_other"}},
	})
	if n := len(ents.Descriptions()); n != 2 {
		t.Errorf("%d entities served, want the two that differ", n)
	}
}

// An add-on installed or removed asks for the lists to be read again before the next connection.
func TestRelistIsHeard(t *testing.T) {
	a := Get()
	a.relist.Store(false)
	component.Relist.Emit(struct{}{})
	if !a.relist.Load() {
		t.Error("Relist was not heard")
	}
	a.relist.Store(false)
}
