package media

import (
	"sync"
	"time"
)

// Position is how far into a track the music is, as its source last said: at the local time At the
// track named Title was Pos into its Dur, moving at Rate (1 normal, 0 paused). Only some sources say:
// Music Assistant over Sendspin does, a station never does. What is shown with the music in time (the
// lyrics) asks for it and goes without when there is none for the track on the page.
type Position struct {
	Title string
	At    time.Time
	Pos   time.Duration
	Dur   time.Duration
	Rate  float64
}

// Now is where the track is at t.
func (p Position) Now(t time.Time) time.Duration {
	d := p.Pos + time.Duration(float64(t.Sub(p.At))*p.Rate)
	if d < 0 {
		return 0
	}
	if p.Dur > 0 && d > p.Dur {
		return p.Dur
	}
	return d
}

var position struct {
	mu  sync.Mutex
	p   Position
	set bool
}

// SetPosition records where the track a source is playing is.
func SetPosition(p Position) {
	position.mu.Lock()
	position.p, position.set = p, true
	position.mu.Unlock()
}

// ClearPosition forgets it, when the source stops saying.
func ClearPosition() {
	position.mu.Lock()
	position.set = false
	position.mu.Unlock()
}

// TrackPosition is the last position recorded, if it is for the track titled title.
func TrackPosition(title string) (Position, bool) {
	position.mu.Lock()
	defer position.mu.Unlock()
	if !position.set || position.p.Title != title || title == "" {
		return Position{}, false
	}
	return position.p, true
}
