package detect

import (
	"math"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
)

// newDirectionEntity is Wake word direction: where the wake word that opened the last turn came from,
// in degrees clockwise from the back of the device (over the power socket) seen from above, which is
// segment 0 of the ring. It is set once, as the word is heard and before the turn opens, so an
// automation on the turn already reads the right value; it then holds until the next one.
//
// Only a board whose array was measured has one (mic.HasDirection): elsewhere there is no bearing worth
// publishing, and the entity is not offered at all. Nil there.
func newDirectionEntity() *esphome.Sensor {
	if !mic.HasDirection() {
		return nil
	}
	return &esphome.Sensor{
		Base: esphome.Base{
			ObjectID: "wake_direction",
			DeviceID: component.DeviceMicrophone,
			Name:     "Wake word direction",
			Icon:     "mdi:compass-outline",
		},
		Unit:     "°",
		Decimals: 0,
	}
}

// setDirection puts where the talker is on the entity, if this board has one and the microphones know.
func setDirection(s *esphome.Sensor) {
	if s == nil {
		return
	}
	at, ok := mic.Get().Facing()
	if !ok {
		return
	}
	s.Set(float32(math.Mod(math.Round(at*360), 360)))
}
