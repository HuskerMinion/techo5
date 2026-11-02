package speaker

import "github.com/HuskerMinion/techo5/echod/internal/lib/beat"

// MusicTap turns the music analysis on or off. It costs a little on every period, so it only runs while
// the ring has been set to follow the music. Turning it off forgets the last reading.
func (p *Player) MusicTap(on bool) {
	if p.musicOn.Swap(on) != on && !on {
		p.music.Reset()
	}
}

// Music is what the music playing now is doing. The tap is on the lanes music goes through (a track's
// queue and an attached Source) before the volume, so the reading follows the music, not the dial; what
// the assistant says, an alarm or a chime never reaches it.
func (p *Player) Music() beat.Snapshot { return p.music.Snapshot() }

// MusicTapping is whether the analysis is running.
func (p *Player) MusicTapping() bool { return p.musicOn.Load() }
