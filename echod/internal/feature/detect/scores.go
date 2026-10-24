package detect

import (
	"fmt"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// Scores is the words being listened for and what they have scored lately, for the setup page
// (wakeword.ReadScores). It copies what the engine's slots hold in one go, and then what the board
// holds, never holding both locks at once: the engine's is held through each frame's inference, and the
// board's is taken inside it.
func (d *Detect) Scores(after uint64, from int64, watch bool) wakeword.Scores {
	now := time.Now()
	e := d.engine
	if watch {
		e.board.watch(now)
	}
	held, muted := e.held()

	slots := make([]int, len(held))
	for i, h := range held {
		slots[i] = h.n
	}
	r := e.board.read(after, from, now, slots)

	sc := wakeword.Scores{Bucket: r.newest, From: r.first, BucketMs: bucketMs, Muted: muted, Lost: r.lost,
		Events: r.events}
	for _, h := range held {
		row := wakeword.ScoreRow{
			Slot: h.n + 1, Role: roleOf(h.n), ID: h.model.ID, Phrase: h.model.Phrase, Ready: h.ready,
			Threshold: d.threshold(h.n), Best: r.best[h.n], Settling: r.settling[h.n],
		}
		row.Cutoff = row.Threshold
		if e.Threshold != nil {
			row.Cutoff = e.Threshold(h.n)
		}
		row.Slack = row.Cutoff < row.Threshold
		sc.Rows = append(sc.Rows, row)
	}
	return sc
}

// threshold is a slot's own threshold, before any allowance for the speaker (thresholdFor).
func (d *Detect) threshold(n int) float64 {
	if n == StopSlot {
		return config.Get().Wake.Stop.Threshold
	}
	if d.base != nil {
		return d.base(n)
	}
	return config.DefaultThreshold
}

// heldSlot is what one slot holds, as Scores shows it.
type heldSlot struct {
	n     int
	model wake.Model
	ready bool
}

// held is every slot with a word in it, and whether the microphones are cut, copied in one hold of the
// lock so a page asking does not wait on a frame once for each slot.
func (e *Engine) held() ([]heldSlot, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	var out []heldSlot
	for i, s := range e.slots {
		if s.loaded {
			out = append(out, heldSlot{n: i, model: s.model, ready: s.scored})
		}
	}
	return out, e.quiet
}

// roleOf is what a slot is for, as a person would name it.
func roleOf(n int) string {
	switch {
	case n < wakeword.Slots:
		return fmt.Sprintf("Wake word %d", n+1)
	case n == StopSlot:
		return "Stop word"
	case n == HangupSlot:
		return "Hang-up word"
	}
	if k := commandKindAt(n); k != nil {
		return strings.ToUpper(k.what[:1]) + k.what[1:]
	}
	return fmt.Sprintf("Slot %d", n+1)
}
