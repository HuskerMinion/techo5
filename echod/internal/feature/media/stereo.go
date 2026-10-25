package media

import (
	"log/slog"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
)

// Stereo is whether the internal speakers play in stereo, false on a board with one driver.
func (p *Player) Stereo() bool { return p.stereo.Get() }

// SetStereo plays athena's two internal speakers in stereo or mono, at once, and saves it. Off is the
// default: stereo can make the wake word harder to catch over music (docs/install-athena.md).
func (p *Player) SetStereo(on bool) {
	settled := speaker.Get().SetStereo(on)
	p.stereo.Set(settled)
	if err := config.Set().Speaker().Stereo(settled); err != nil {
		slog.Error("saving a setting failed", "setting", p.stereo.ObjectID, "err", err)
	}
	slog.Info("setting changed", "setting", p.stereo.ObjectID, "using", settled)
}
