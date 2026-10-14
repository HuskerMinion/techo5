package media

import (
	"fmt"
	"log/slog"
	"math"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func (p *Player) commandSpeakerGain(v float32) {
	if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) ||
		v < 0 || v > config.SpeakerGainMax || math.Trunc(float64(v)) != float64(v) {
		p.gain.Set(p.gain.Get())
		return
	}
	if err := p.SetSpeakerGain(int(v)); err != nil {
		slog.Error("changing speaker gain failed", "err", err)
		p.gain.Set(p.gain.Get())
	}
}

// SpeakerGain is separate from the volume slider and tone controls. It affects
// all sound from the internal amplifier, including replies, alarms and radio.
func (p *Player) SpeakerGainAvailable() bool { return p.gain != nil }

func (p *Player) SpeakerGain() int {
	if p.gain == nil {
		return 0
	}
	return int(p.gain.Get())
}

// SetSpeakerGain is shared by the screen, setup page and Home Assistant. Publish
// only after both the amplifier and saved state agree; a failed save undoes the write.
func (p *Player) SetSpeakerGain(db int) error {
	p.gainMu.Lock()
	defer p.gainMu.Unlock()
	if p.gain == nil {
		return fmt.Errorf("speaker gain is not supported on this amplifier")
	}
	if db < 0 || db > config.SpeakerGainMax {
		return fmt.Errorf("speaker gain must be between 0 and %d dB", config.SpeakerGainMax)
	}
	old := p.SpeakerGain()
	if err := p.gainApply(db); err != nil {
		return err
	}
	if err := config.Set().Speaker().GainDB(db); err != nil {
		if rollback := p.gainApply(old); rollback != nil {
			slog.Error("restoring amplifier gain after a failed save failed", "err", rollback)
		}
		return err
	}
	p.gain.Set(float32(db))
	slog.Info("setting changed", "setting", p.gain.ObjectID, "using", db)
	return nil
}
