package speaker

import (
	"math"
	"testing"
)

// loudBlock is a period of a 220 Hz tone, interleaved.
func loudBlock() []int16 {
	out := make([]int16, period*Channels)
	for i := 0; i < period; i++ {
		v := int16(8000 * math.Sin(2*math.Pi*220*float64(i)/Rate))
		out[i*2], out[i*2+1] = v, v
	}
	return out
}

func playFor(t *testing.T, p *Player, blocks int, queue func(*Player)) {
	t.Helper()
	for i := 0; i < blocks; i++ {
		queue(p)
		played(t, p)
	}
}

func TestMusicTapHearsTheTrackAndNotTheQueue(t *testing.T) {
	p := newTestPlayer()
	p.MusicTap(true)

	// A reply, a chime or an alarm goes through the queue.
	playFor(t, p, 100, func(p *Player) { p.pending = append(p.pending, loudBlock()...) })
	if m := p.Music(); m.Playing || m.Level != 0 {
		t.Fatalf("the queue reached the music analysis: %+v", m)
	}

	// A track goes through the bed.
	playFor(t, p, 100, func(p *Player) { p.bed = append(p.bed, loudBlock()...) })
	if m := p.Music(); !m.Playing || m.Level < 0.5 {
		t.Fatalf("a track did not: %+v", m)
	}
}

func TestMusicTapHearsAnAttachedSource(t *testing.T) {
	p := newTestPlayer()
	p.MusicTap(true)
	p.Attach(&recorder{fill: 6000})
	playFor(t, p, 100, func(*Player) {})
	if m := p.Music(); !m.Playing {
		t.Fatalf("a Source did not: %+v", m)
	}
}

func TestMusicTapIsOffUntilAsked(t *testing.T) {
	p := newTestPlayer()
	playFor(t, p, 100, func(p *Player) { p.bed = append(p.bed, loudBlock()...) })
	if m := p.Music(); m.Playing {
		t.Fatalf("analyzed without being asked: %+v", m)
	}

	p.MusicTap(true)
	playFor(t, p, 100, func(p *Player) { p.bed = append(p.bed, loudBlock()...) })
	p.MusicTap(false)
	if m := p.Music(); m.Playing || p.MusicTapping() {
		t.Fatalf("still reading after being turned off: %+v", m)
	}
}
