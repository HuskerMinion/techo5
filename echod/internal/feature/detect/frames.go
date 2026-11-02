package detect

import (
	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
)

// frameEntities say how the wake word loop keeps up, from what the "wake frames" log line works out:
// what the wake word costs (Wake word load), whether frames are being lost (Wake frames, fifty a second
// when none are), and the stalls only the log showed before (Worst wake frame).
type frameEntities struct {
	busy, rate, worst *esphome.Sensor
}

func newFrameEntities() frameEntities {
	sensor := func(id, name, icon, unit string, decimals int32) *esphome.Sensor {
		return &esphome.Sensor{
			Base: esphome.Base{
				ObjectID: id, DeviceID: component.DeviceMicrophone, Name: name, Icon: icon,
				Category: esphome.CategoryDiagnostic,
			},
			Unit:       unit,
			StateClass: esphome.StateClassMeasurement,
			Decimals:   decimals,
		}
	}
	return frameEntities{
		busy:  sensor("wake_word_load", "Wake word load", "mdi:speedometer", "%", 0),
		rate:  sensor("wake_frames", "Wake frames", "mdi:waveform", "/s", 1),
		worst: sensor("worst_wake_frame", "Worst wake frame", "mdi:timer-alert-outline", "ms", 0),
	}
}

func (l frameEntities) set(f Frames) {
	l.busy.Set(float32(f.Busy))
	l.rate.Set(float32(f.PerSecond))
	l.worst.Set(float32(f.Worst.Milliseconds()))
}
