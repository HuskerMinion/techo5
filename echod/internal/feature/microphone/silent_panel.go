//go:build panel

package microphone

import (
	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/lib/micsilent"
)

// silentSensor is a panel's microphone gone silent, as a problem in Home Assistant: Android can take a
// tablet's microphone away (its privacy switch, another app) and hand the app nothing but silence, and
// then the wake word cannot be heard here. The capture decides it (hardware/mic, flat_panel.go) and
// clears it at the first sound.
var silentSensor = &esphome.BinarySensor{
	Base: esphome.Base{ObjectID: "microphone_silent", Name: "Microphone silenced", Icon: "mdi:microphone-off",
		Category: esphome.CategoryDiagnostic},
	DeviceClass: "problem",
}

func init() {
	micsilent.Changed.Listen(func(on bool) { silentSensor.Set(on) })
}

// buildEntities is what only a panel puts up beside the shared knobs.
func buildEntities() []esphome.Entity {
	silentSensor.Set(micsilent.Silent())
	return []esphome.Entity{silentSensor}
}
