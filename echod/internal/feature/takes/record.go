package takes

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
)

// frameTime is one frame from the microphones (mic.FrameSamples at mic.Rate).
const frameTime = time.Second * mic.FrameSamples / mic.Rate

// frames is how long n frames are.
func frames(n int) time.Duration { return time.Duration(n) * frameTime }

// record is a recording: the frames the wake word hears, kept until it is stopped, runs out, or is
// ended by something that has the microphones a better claim to them. It runs on the frame the engine
// scores (mic.Listen), so the takes are what the wake word itself heard, mixed and leveled.
//
// The loop only copies: a frame, its level and whether the device was making a sound at that moment.
// The listener's channel holds eight frames, and anything slower would lose some. The cutting is done
// once at the end, and roughly once a second for the count the page shows.
func (f *Feature) record(ctx context.Context, id uint64, finish <-chan struct{}) {
	frameCh, stop := f.dev.listen("takes")
	f.note(startNotes...)

	most := int(Longest / frameTime)
	pcm := make([]int16, 0, most*mic.FrameSamples)
	level := make([]float32, 0, most)
	over := make([]bool, 0, most)

	started := f.dev.now()
	last, recount := started, started.Add(time.Second)
	t := time.NewTicker(check)
	keep, why := true, ""

	look := func() bool {
		if w, k := f.over(); w != "" {
			keep, why = k, w
			return true
		}
		at := f.dev.now()
		switch {
		case at.Sub(last) > gap:
			why = "the microphones paused"
			return true
		case at.Sub(started) >= Longest+leadIn:
			why = "two minutes passed"
			return true
		}
		if at.After(recount) {
			recount = at.Add(time.Second)
			takes, _ := cut(level, over)
			f.mu.Lock()
			if f.id == id {
				f.about = len(takes)
			}
			f.mu.Unlock()
			f.Changed.Emit(struct{}{}) // the time and the count
		}
		return false
	}

loop:
	for {
		select {
		case <-ctx.Done():
			keep, why = false, ""
			break loop
		case <-finish:
			why = "stopped"
			break loop
		case fr, ok := <-frameCh:
			if !ok {
				why = "the microphones stopped"
				break loop
			}
			last = f.dev.now()
			if last.Sub(started) < leadIn {
				continue
			}
			// The ceiling by the count as well as the clock: frames arriving faster than the clock after
			// a stall must not grow the recording past what was set aside for it.
			if len(pcm)+len(fr) > cap(pcm) {
				why = "two minutes passed"
				break loop
			}
			pcm = append(pcm, fr...) // a copy: the frame is shared with every other listener
			level = append(level, rms(fr))
			over = append(over, f.dev.audible())
		case <-f.poke:
			if look() {
				break loop
			}
		case <-t.C:
			if look() {
				break loop
			}
		}
	}
	t.Stop()
	stop()
	f.release(id)
	f.note(stopNotes...)

	if !keep {
		f.mu.Lock()
		if f.id == id {
			f.phase, f.why, f.cancel = Idle, why, nil
			f.forget()
		}
		f.mu.Unlock()
		if why != "" {
			slog.Info("takes: thrown away", "why", why)
		}
		f.Changed.Emit(struct{}{})
		return
	}

	takes, left := cut(level, over)
	// More than one frame in a hundred short of the clock, up to the last frame, is frames lost on the
	// way (mic.go reports the same as "listener behind"): the takes may have holes in them. A pause that
	// ended the recording is after the last frame, and is not counted.
	recorded := frames(len(level))
	lost := short(recorded, last.Sub(started))
	f.mu.Lock()
	if f.id != id {
		f.mu.Unlock()
		return // thrown away while it wound down
	}
	f.phase, f.why, f.cancel = Kept, why, nil
	f.pcm, f.cuts, f.left, f.lost = pcm, takes, left, lost
	f.elapsed, f.about = recorded, len(takes)
	f.mu.Unlock()
	slog.Info("takes: kept", "takes", len(takes), "left out", len(left), "seconds", recorded.Round(time.Second).Seconds(),
		"why", why, "frames lost", lost)
	f.Changed.Emit(struct{}{})
}

// over is why a running recording has to end besides being stopped or running out, and whether what
// was recorded before then is kept: empty while it may go on. A ring keeps the takes, since it has
// nothing to do with them and must stay stoppable; the rest have the microphones a better claim, or
// take them away.
func (f *Feature) over() (why string, keep bool) {
	if !f.dev.switchedOn() {
		return "Record takes of a word was switched off", false
	}
	if m, err := f.dev.muted(); err != nil || m {
		return "the microphones were muted", false
	}
	switch {
	case f.dev.inCall():
		return "a call", false
	case f.dev.talking():
		return "a talk through a camera", false
	case f.dev.silent():
		return "the microphones went silent", false
	case f.dev.announcing():
		return "an announcement started", false
	case f.dev.ringing():
		return "an alarm or timer rang", true
	}
	return "", false
}

// rms is a frame's level, in the samples' own units.
func rms(frame []int16) float32 {
	if len(frame) == 0 {
		return 0
	}
	var sum float64
	for _, s := range frame {
		sum += float64(s) * float64(s)
	}
	return float32(math.Sqrt(sum / float64(len(frame))))
}

// short is whether frames went missing on the way: what was recorded is more than one frame in a hundred
// short of the time from the first frame kept to the last, with the lead-in dropped from the front.
func short(recorded, toLast time.Duration) bool {
	return float64(recorded) < 0.99*float64(toLast-leadIn-frames(1))
}
