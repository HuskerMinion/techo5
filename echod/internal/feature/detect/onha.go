package detect

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
)

// onHAEntities are the settings for Home Assistant's own wake word engine (config.OnHA): streaming to
// it, confirming with it, and the lowest score worth confirming. Both switches start off: either one
// sends the room's audio to Home Assistant, and that is the owner's choice to make.
type onHAEntities struct {
	verify    *esphome.Switch
	stream    *esphome.Switch
	confirm   *esphome.Switch
	threshold *esphome.Number
	sttSent   *esphome.Sensor
	sttAllow  *esphome.Sensor
	sttBlock  *esphome.Sensor
}

func newOnHAEntities(cancelVerification func()) onHAEntities {
	o := onHAEntities{
		sttSent:  newSTTWakeCounter("wake_stt_requests_sent", "Wake STT requests sent", "mdi:microphone-message"),
		sttAllow: newSTTWakeCounter("wake_stt_allowed", "Wake STT allowed", "mdi:check-circle-outline"),
		sttBlock: newSTTWakeCounter("wake_stt_blocked", "Wake STT blocked", "mdi:close-circle-outline"),
		verify: &esphome.Switch{
			Base: esphome.Base{
				ObjectID: "always_verify_with_stt",
				Name:     "Always Verify with STT",
				Icon:     "mdi:account-check",
				Category: esphome.CategoryConfig,
			},
		},
		stream: &esphome.Switch{
			Base: esphome.Base{
				ObjectID: "wake_word_on_home_assistant",
				DeviceID: component.DeviceMicrophone,
				Name:     "Wake word on Home Assistant",
				Icon:     "mdi:home-sound-in",
				Category: esphome.CategoryConfig,
			},
		},
		confirm: &esphome.Switch{
			Base: esphome.Base{
				ObjectID: "confirm_wake_with_home_assistant",
				DeviceID: component.DeviceMicrophone,
				Name:     "Confirm wake with Home Assistant",
				Icon:     "mdi:account-check",
				Category: esphome.CategoryConfig,
			},
		},
		threshold: &esphome.Number{
			Base: esphome.Base{
				ObjectID: "confirm_threshold",
				DeviceID: component.DeviceMicrophone,
				Name:     "Confirm threshold",
				Icon:     "mdi:tune-vertical",
				Category: esphome.CategoryConfig,
			},
			Min: 0.5, Max: 0.95, Step: 0.01,
			Mode: esphome.NumberBox,
		},
	}

	o.verify.OnCommand = func(on bool) {
		if err := config.Set().OnHA().VerifySTT(on); err != nil {
			slog.Error("saving STT wake verification failed", "err", err)
			o.verify.Set(config.Get().Wake.OnHA.VerifySTT)
			return
		}
		if !on {
			cancelVerification()
		}
		o.verify.Set(on)
		voice.Get().WakeOnHAChanged()
	}
	o.stream.OnCommand = func(on bool) {
		o.stream.Set(on)
		if err := config.Set().OnHA().Stream(on); err != nil {
			slog.Error("saving the wake word on home assistant failed", "err", err)
		}
		slog.Info("wake word on home assistant", "on", on)
		voice.Get().WakeOnHAChanged()
	}
	o.confirm.OnCommand = func(on bool) {
		o.confirm.Set(on)
		if err := config.Set().OnHA().Confirm(on); err != nil {
			slog.Error("saving confirm wake with home assistant failed", "err", err)
		}
		slog.Info("confirm wake with home assistant", "on", on)
		voice.Get().WakeOnHAChanged()
	}
	o.threshold.OnCommand = func(v float32) {
		o.threshold.Set(v)
		if err := config.Set().OnHA().ConfirmThreshold(float64(v)); err != nil {
			slog.Error("saving the confirm threshold failed", "err", err)
		}
	}
	return o
}

func (o onHAEntities) restore(c config.OnHA) {
	o.verify.Set(c.VerifySTT)
	o.publishSTTWakes(c.STTWakes)
	o.stream.Set(c.Stream)
	o.confirm.Set(c.Confirm)
	o.threshold.Set(float32(c.ConfirmAt()))
	slog.Info("restored", "what", "wake word on home assistant", "stream", c.Stream, "confirm", c.Confirm,
		"confirm_threshold", c.ConfirmAt())
}

func newSTTWakeCounter(id, name, icon string) *esphome.Sensor {
	return &esphome.Sensor{
		Base:       esphome.Base{ObjectID: id, Name: name, Icon: icon},
		StateClass: esphome.StateClassTotalIncreasing,
	}
}

func (o onHAEntities) publishSTTWakes(counts config.STTWakeCounts) {
	o.sttSent.Set(float32(counts.Sent))
	o.sttAllow.Set(float32(counts.Allowed))
	o.sttBlock.Set(float32(counts.Blocked))
}
