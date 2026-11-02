package media

import (
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// Voice volume and call volume: the assistant's voice (replies, announcements, what the device says) and
// a call's audio (the intercom and the phone) each sit at a level of their own beside the volume, which
// still moves music and what the buttons and Home Assistant's media player turn. Each is an offset in
// steps from the volume, zero for the volume itself: the one setting that leaves a device nobody touches
// exactly as it was, and follows the volume up and down, which a level of its own would not. Where the
// ring volume is a level (an alarm has to sound with the music off), these are for things said over or
// beside the music, so quiet replies with loud music, or the other way round, is a matter of an offset.
//
// A volume at zero or muted keeps both silent, and quiet hours stop a raise at the night volume
// (speaker.LaneStep). The speaker works out the steps itself from the volume it is at, so the buttons
// move all three together; this only tells it the offsets and the ceiling.

func newLaneOffset(id, name, icon string) *esphome.Number {
	return &esphome.Number{
		Base: esphome.Base{ObjectID: id, Name: name, Icon: icon, Category: esphome.CategoryConfig},
		Min:  -config.VolumeSteps, Max: config.VolumeSteps, Step: 1, Unit: "steps",
		Mode: esphome.NumberSlider,
	}
}

// SetVoiceOffset sets how far the assistant's voice sits from the volume, in steps.
func (p *Player) SetVoiceOffset(n int) {
	n = config.ClampOffset(n)
	if err := config.Set().Speaker().VoiceOffset(n); err != nil {
		slog.Error("saving a setting failed", "setting", p.voiceVol.ObjectID, "err", err)
		return
	}
	p.voiceVol.Set(float32(n))
	p.pushLanes()
	slog.Info("setting changed", "setting", p.voiceVol.ObjectID, "using", n)
}

// SetCallOffset sets how far a call's audio sits from the volume, in steps.
func (p *Player) SetCallOffset(n int) {
	n = config.ClampOffset(n)
	if err := config.Set().Speaker().CallOffset(n); err != nil {
		slog.Error("saving a setting failed", "setting", p.callVol.ObjectID, "err", err)
		return
	}
	p.callVol.Set(float32(n))
	p.pushLanes()
	slog.Info("setting changed", "setting", p.callVol.ObjectID, "using", n)
}

// VoiceOffset and CallOffset are the offsets now, for the setup page.
func (p *Player) VoiceOffset() int { return config.Get().Speaker.VoiceOffset }
func (p *Player) CallOffset() int  { return config.Get().Speaker.CallOffset }

// pushLanes tells the speaker the offsets and the night ceiling. Called when any of them can have
// changed: a setting, the hours starting or ending, a restore.
func (p *Player) pushLanes() {
	c := config.Get().Speaker
	ceiling := 0
	if quietNow() {
		ceiling = c.NightVolume
	}
	speaker.Get().SetLaneOffsets(c.VoiceOffset, c.CallOffset)
	speaker.Get().SetLaneCeiling(ceiling)
}
