package voice

import (
	"context"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

type verifiedCue struct {
	ctx    context.Context
	slot   int
	done   chan struct{}
	result chan time.Time
	cancel context.CancelFunc
}

func (c *conversation) startVerifiedCue(cue *verifiedCue) {
	if cue.ctx.Err() != nil || c.cue != nil || c.phase != phaseIdle ||
		c.pending != nil || c.watch != nil || c.draining ||
		!config.Get().Wake.OnHA.VerifySTT || !c.ha.Ready() || c.micBusy() || !haAnswers(cue.slot) {
		close(cue.done)
		return
	}
	ctx, cancel := context.WithCancel(cue.ctx)
	cue.cancel = cancel
	cue.result = make(chan time.Time, 1)
	c.cue = cue
	// Only the display changes at cue start. The actual listening phase starts
	// once the audible cue ends. The wake word and the loud part of the cue
	// stay out of the command audio.
	c.shown = State{Phase: phaseListening.String()}
	Changed.Emit(c.shown)
	safe.Go("verified wake cue", func() {
		if ctx.Err() == nil && ring.IsSounding() {
			ring.Silence()
		}
		at, ok := wakeword.VerifiedChime(ctx, cue.slot)
		if !ok || ctx.Err() != nil {
			at = time.Time{}
		}
		cue.result <- at
	})
}

func (c *conversation) finishVerifiedCue(at time.Time) {
	cue := c.cue
	if cue == nil {
		return
	}
	c.cue = nil
	defer close(cue.done)
	defer cue.cancel()
	if !at.IsZero() && cue.ctx.Err() == nil &&
		c.start(nextTurn{slot: cue.slot, woke: true, verified: true, at: at}) {
		return
	}
	if c.phase == phaseIdle && c.shown.Phase == phaseListening.String() {
		c.enter(phaseIdle)
	}
}
