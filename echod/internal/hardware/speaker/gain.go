package speaker

import (
	"fmt"
	"sync"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
)

// Only the TAS5805M's integer gain scale has been checked: 110 is 0 dB and each
// step is 1 dB. Other Show 5 units use a different amplifier and must not get this write.
const gainControl = "Master TAS5805m Playback Volume A"
const gainUnity = 110

type gainMixer interface {
	Find(string) (alsa.Control, error)
	Set(alsa.Control, uint32) error
}

func gainControlOn(m gainMixer) (alsa.Control, error) {
	c, err := m.Find(gainControl)
	if err != nil {
		return c, err
	}
	if c.Type != alsa.TypeInteger || c.Count != 1 || c.Min > gainUnity ||
		c.Max < gainUnity+config.SpeakerGainMax || c.Step > 1 {
		return c, fmt.Errorf("speaker: unsupported amplifier gain control")
	}
	return c, nil
}

// GainAvailable checks the actual mixer, not the model name: not every 2nd gen
// Show 5 has this amplifier. Enumeration is cached and happens only once per process.
var GainAvailable = sync.OnceValue(func() bool {
	m, err := alsa.OpenMixer(Card)
	if err != nil {
		return false
	}
	defer m.Close()
	_, err = gainControlOn(m)
	return err == nil
})

func writeSpeakerGain(m gainMixer, db int) error {
	if db < 0 || db > config.SpeakerGainMax {
		return fmt.Errorf("speaker gain must be between 0 and %d dB", config.SpeakerGainMax)
	}
	c, err := gainControlOn(m)
	if err != nil {
		return err
	}
	return m.Set(c, uint32(gainUnity+db))
}

// SetSpeakerGain writes the amplifier directly. Before Start it remembers the
// requested gain; Run reapplies it before any audio, including after service restarts.
func (p *Player) SetSpeakerGain(db int) error {
	if db < 0 || db > config.SpeakerGainMax {
		return fmt.Errorf("speaker gain must be between 0 and %d dB", config.SpeakerGainMax)
	}
	if !GainAvailable() {
		return fmt.Errorf("speaker gain is not supported on this amplifier")
	}
	p.gainMu.Lock()
	defer p.gainMu.Unlock()
	_, m := p.device()
	if m != nil {
		if err := writeSpeakerGain(m, db); err != nil {
			return err
		}
	}
	p.gainDB = db
	return nil
}

func (p *Player) applySpeakerGain() error {
	p.gainMu.Lock()
	defer p.gainMu.Unlock()
	_, m := p.device()
	if m == nil {
		return fmt.Errorf("speaker: mixer is not open")
	}
	return writeSpeakerGain(m, p.gainDB)
}
