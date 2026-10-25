package speaker

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
		return c, errGainShape
	}
	return c, nil
}

// errGainShape is a gain control there, but not the TAS5805M's scale this writes.
var errGainShape = errors.New("speaker: unsupported amplifier gain control")

// gainAnswer is what gainControlOn's err says about the control: there (ok), or sure not to be, the
// mixer read whole without it or with one of another shape. Any other error is a mixer that could not
// be read (an ioctl failing, the mixer closed under it), which says nothing, so it is asked again.
func gainAnswer(err error) (ok, sure bool) {
	switch {
	case err == nil:
		return true, true
	case errors.Is(err, alsa.ErrNoControl), errors.Is(err, errGainShape):
		return false, true
	}
	return false, false
}

// GainAvailable checks the actual mixer, not the model name: not every 2nd gen
// Show 5 has this amplifier. The mixer walk takes over a second, so a build or a
// board without the amplifier (tas5805mListed) skips it. An answer is kept for the
// process once it is sure: the amplifier's control found, or the amplifier absent.
// A card that could not be opened yet is asked again next time.
var GainAvailable = func() bool {
	if gainOnPath {
		return true // the gain goes with the path: the Dot codec's driver gain, athena's amplifier volume
	}
	gainSeen.mu.Lock()
	defer gainSeen.mu.Unlock()
	if gainSeen.sure {
		return gainSeen.ok
	}
	ok, sure := findGain(mayHaveTAS5805M && tas5805mListed(i2cDevices), openGainMixer)
	gainSeen.ok, gainSeen.sure = ok, sure
	return ok
}

// GainPossible is whether the amplifier's gain control may be there: GainAvailable's sure yes, or
// not sure yet on a board that may have the amplifier. It never walks the mixer, so the media player
// can build its gain control while the card is not up yet, and a late sure yes still finds the saved
// gain waiting (SetSpeakerGain) and the control there.
var GainPossible = func() bool {
	if gainOnPath {
		return true
	}
	gainSeen.mu.Lock()
	defer gainSeen.mu.Unlock()
	if gainSeen.sure {
		return gainSeen.ok
	}
	return mayHaveTAS5805M && tas5805mListed(i2cDevices)
}

// gainOn is GainAvailable, asked of m, the mixer the speaker already holds, when the card could not
// be opened on its own to say: a card whose playback device is held is up, so what m says is sure,
// and kept as GainAvailable's answer. Without it, a gain saved while the answer was not sure waited
// for the next start of the speaker to be written.
func gainOn(m gainMixer) bool {
	if GainAvailable() {
		return true
	}
	if m == nil || !GainPossible() {
		return false
	}
	gainSeen.mu.Lock()
	defer gainSeen.mu.Unlock()
	if gainSeen.sure {
		return gainSeen.ok
	}
	_, err := gainControlOn(m)
	gainSeen.ok, gainSeen.sure = gainAnswer(err)
	return gainSeen.ok
}

// heldMixer is the mixer the speaker holds, as a gainMixer: nil, not a nil *alsa.Mixer, when it holds none.
func (p *Player) heldMixer() gainMixer {
	if _, m := p.device(); m != nil {
		return m
	}
	return nil
}

var gainSeen struct {
	mu       sync.Mutex
	ok, sure bool
}

type gainMixerCloser interface {
	gainMixer
	Close() error
}

// openGainMixer opens the card's mixer; a variable for the tests.
var openGainMixer = func() (gainMixerCloser, error) {
	m, err := alsa.OpenMixer(Card)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// findGain is whether the amplifier's gain control is there, and whether that is
// sure. A board that cannot have the amplifier is a sure no without the walk; a
// mixer that does not open is not sure, since the card may not be up yet. Once it
// is, the codecs are all on it, so a missing control is a sure no.
func findGain(mayHave bool, open func() (gainMixerCloser, error)) (ok, sure bool) {
	if !mayHave {
		return false, true
	}
	m, err := open()
	if err != nil {
		return false, false
	}
	defer m.Close()
	_, err = gainControlOn(m)
	return gainAnswer(err)
}

// i2cDevices lists the I2C devices the device tree describes, each with its name:
// the amplifier's is "tas5805m" (compatible "ti,tas5805m"), bound to its driver or
// not. A variable for the tests.
var i2cDevices = "/sys/bus/i2c/devices"

// tas5805mListed is whether the board may have the amplifier: a device named
// tas5805m in dir. It says no only when the list was read and none is there; a
// list that cannot be read, or is empty, leaves the mixer to say.
func tas5805mListed(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return true
	}
	for _, e := range entries {
		name, err := os.ReadFile(filepath.Join(dir, e.Name(), "name"))
		if err != nil {
			return true
		}
		if strings.TrimSpace(string(name)) == "tas5805m" {
			return true
		}
	}
	return false
}

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

// SetSpeakerGain writes the amplifier directly. Before Start, or while the card
// is not up and the control not yet sure to be there (GainPossible), it remembers
// the requested gain; Run reapplies it before any audio, including after service
// restarts, once GainAvailable is sure.
func (p *Player) SetSpeakerGain(db int) error {
	if db < 0 || db > config.SpeakerGainMax {
		return fmt.Errorf("speaker gain must be between 0 and %d dB", config.SpeakerGainMax)
	}
	if !GainPossible() {
		return errNoGain
	}
	if gainOnPath {
		return p.setPathGain(db)
	}
	p.gainMu.Lock()
	defer p.gainMu.Unlock()
	if _, m := p.device(); m != nil {
		if gainOn(m) {
			if err := writeSpeakerGain(m, db); err != nil {
				return err
			}
		} else if !GainPossible() {
			return errNoGain
		}
	}
	p.gainDB = db
	return nil
}

var errNoGain = fmt.Errorf("speaker gain is not supported on this amplifier")

// pathGainMixer is the one write setPathGain makes; *alsa.Mixer, or a stand-in in the tests.
type pathGainMixer interface {
	SetInt(name string, v uint32) error
}

// heldPathMixer is the mixer setPathGain writes to: the one the speaker holds, or nil before Start.
// A variable for the tests.
var heldPathMixer = func(p *Player) pathGainMixer {
	if _, m := p.device(); m != nil {
		return m
	}
	return nil
}

// setPathGain is SetSpeakerGain where the gain goes with the speaker's path (gainOnPath). It is kept
// for every path written from now on (Player.path), and written at once if the speaker is the output
// and the mixer is held. On the jack, or before Start, it waits for the speaker's path to be written.
// gainDB is changed under pathMu as well as gainMu, so a path being written reads one value or the
// other, never half of a change. It is kept first: a write that fails now is said in the log, and the
// gain still goes on with the speaker's next path, as it would have from the jack.
func (p *Player) setPathGain(db int) error {
	p.gainMu.Lock()
	defer p.gainMu.Unlock()
	p.pathMu.Lock()
	defer p.pathMu.Unlock()
	p.gainDB = db
	if m := heldPathMixer(p); m != nil && p.out == OutputSpeaker && !p.draining {
		for _, c := range speakerGainControls(db) {
			if err := m.SetInt(c.name, uint32(c.level)); err != nil {
				slog.Warn("speaker gain: writing it now failed; it goes on with the next path", "db", db, "err", err)
				break
			}
		}
	}
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
