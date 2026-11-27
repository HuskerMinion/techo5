//go:build !dot

package presence

import (
	"log/slog"
	"time"
)

// The log's account of the looks passed over while nobody is near (#739): somebody standing at the
// device unseen for minutes left nothing to say whether it was the light, the distance or the room's
// noise. A minute of looks is summed up in one line, and only a minute that had something to explain:
// movement short of what makes somebody near, or enough looks not compared. A still, empty room says
// nothing, and nothing is said while somebody is near.
const (
	passEvery = time.Minute

	// passedMin is how many looks not compared in a minute (of about 120) make it worth a line: a light
	// switched on swings three to five of them, the screen's own change of light about four.
	passedMin = 10
)

// passStats sums up the room detector's looks over one passEvery.
type passStats struct {
	from    time.Time // the first look summed; zero when nothing is
	hushAt  time.Time // the last frame counted as hushed: they are counted at the room's rate
	looks   int       // looks taken, compared or not
	counted int       // looks compared
	moved   int       // compared looks with movement, not (yet) two of three
	swing   int       // not compared: the whole picture changed (lookSwing)
	bright  int       // not compared: too bright (lookBright)
	hushed  int       // not compared: the device's own light changing (Hush)
	peak    int       // the largest patch that moved in any look
	glare   int       // the most cells too bright to show a change, in any look
}

// add sums up a look the detector took at at.
func (p *passStats) add(at time.Time, l look, movement bool) {
	p.start(at)
	p.looks++
	switch l.kind {
	case lookSwing:
		p.swing++
	case lookBright:
		p.bright++
	case lookCounted:
		p.counted++
		if movement {
			p.moved++
		}
		p.peak = max(p.peak, l.patch)
		p.glare = max(p.glare, l.glare)
	}
}

// hush counts a frame passed over for the device's own light, at most one in roomEvery, as the room's
// looks are taken.
func (p *passStats) hush(at time.Time) {
	if at.Sub(p.hushAt) < roomEvery {
		return
	}
	p.start(at)
	p.hushAt = at
	p.hushed++
}

func (p *passStats) start(at time.Time) {
	if p.from.IsZero() {
		p.from = at
	}
}

// worth is whether the minute had something to explain: a patch at least half of need moved, or
// passedMin looks were not compared.
func (p *passStats) worth(need int) bool {
	return p.moved > 0 || 2*p.peak >= need || p.swing+p.bright+p.hushed >= passedMin
}

// due ends the minute at now, if it is over: it gives the minute's sum and whether it is worth a line,
// and starts the next.
func (p *passStats) due(now time.Time, need int) (passStats, bool) {
	if p.from.IsZero() || now.Sub(p.from) < passEvery {
		return passStats{}, false
	}
	done := *p
	*p = passStats{hushAt: p.hushAt}
	return done, done.worth(need)
}

// reset drops the minute so far: somebody came near, or the watching started over.
func (p *passStats) reset() { *p = passStats{} }

// logPassed is the line for a minute worth one: how much moved against what was needed, the bar a cell
// had to clear (and how many cells their own noise had raised above it, and how high), and the looks
// not compared, by kind.
func logPassed(p passStats, need int, bar float64, raised int, top float64) {
	slog.Info("presence: passing over looks",
		"looks", p.looks, "counted", p.counted, "moved", p.moved,
		"peak", p.peak, "need", need,
		"bar", int(bar), "raised", raised, "top", int(top),
		"swing", p.swing, "bright", p.bright, "glare", p.glare, "hushed", p.hushed)
}
