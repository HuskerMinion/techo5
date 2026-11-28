package detect

import (
	"log/slog"
	"math"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
)

// direction is Wake word direction and the switch that sends it.
//
// The sensor is where the wake word that opened the last turn came from, in degrees clockwise from the
// back of the device (over the power socket) seen from above, which is segment 0 of the ring. It is set
// once, as the word is heard and before the turn opens, so an automation on the turn already reads the
// right value; it then holds until the next one.
//
// Send wake word direction is off unless chosen: a bearing on every wake says where somebody is in the
// room, and Motion and Presence detection, which say as much, are off until turned on too. Off, the
// sensor is unknown and nothing is set on it.
type direction struct {
	sensor *esphome.Sensor
	send   *esphome.Switch
}

// newDirection is Wake word direction and its switch. Only a board whose array was measured has them
// (mic.HasDirection): elsewhere there is no bearing worth publishing, and neither is offered. Nil there.
func newDirection() *direction {
	if !mic.HasDirection() {
		return nil
	}
	d := &direction{
		sensor: &esphome.Sensor{
			Base: esphome.Base{
				ObjectID: "wake_direction",
				DeviceID: component.DeviceMicrophone,
				Name:     "Wake word direction",
				Icon:     "mdi:compass-outline",
			},
			Unit:     "°",
			Decimals: 0,
		},
		send: &esphome.Switch{
			Base: esphome.Base{
				ObjectID: "send_wake_direction",
				DeviceID: component.DeviceMicrophone,
				Name:     "Send wake word direction",
				Icon:     "mdi:compass-off-outline",
				Category: esphome.CategoryConfig,
			},
		},
	}
	d.send.OnCommand = d.setSend
	return d
}

// entities is the sensor and its switch, or nothing on a board without them.
func (d *direction) entities() []esphome.Entity {
	if d == nil {
		return nil
	}
	return []esphome.Entity{d.sensor, d.send}
}

// restore shows the saved choice. The finder is kept listening while it is on (mic.KeepFacing), so the
// bearing is there as the word ends whichever mix is chosen.
func (d *direction) restore(c config.Config) {
	if d == nil {
		return
	}
	d.apply(c.Wake.SendDirection)
}

// setSend is Send wake word direction turned on or off, from Home Assistant.
func (d *direction) setSend(on bool) {
	if err := config.Set().SendDirection(on); err != nil {
		slog.Error("saving a setting failed", "setting", "send wake direction", "err", err)
		d.send.Set(!on)
		return
	}
	d.apply(on)
	slog.Info("setting changed", "setting", "send wake direction", "using", on)
}

// apply shows on, and off empties the sensor at once, so the last bearing does not sit there afterwards.
func (d *direction) apply(on bool) {
	d.send.Set(on)
	mic.Get().KeepFacing("wake direction", on)
	if !on {
		d.sensor.Set(float32(math.NaN()))
	}
}

// set puts where the talker is on the sensor, if this board has one, it is sent, and the microphones
// know.
func (d *direction) set() {
	if d == nil || !config.Get().Wake.SendDirection {
		return
	}
	at, ok := mic.Get().Facing()
	if !ok {
		return
	}
	d.sensor.Set(float32(math.Mod(math.Round(at*360), 360)))
}
