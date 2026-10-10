package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// Loading a dashboard is the dear part: on a Raspberry Pi a heavy one keeps Chrome busy for tens of
// seconds. So a screen that goes away does not take its tab with it at once. The tab is parked -
// its stream stopped, the page frozen so its scripts stop too - and a screen that comes back for
// the same dashboard within warmFor gets it back, thawed, without loading anything. A device
// restarting, a dashboard switched away from and back, the connection dropping for a moment: none
// of them is a reload.

const (
	warmFor   = 5 * time.Minute
	warmCount = 4 // tabs parked at once, the oldest closed to make room
)

// warmTab is a tab and whoever is watching its frames: the session using it, or nobody while it is
// parked. A tab's frames arrive through one listener for its whole life, since a listener cannot be
// taken off again.
type warmTab struct {
	ctx   context.Context
	close func()

	mu      sync.Mutex
	onFrame func(*page.EventScreencastFrame)
	key     string
	expiry  *time.Timer
	stuck   bool // it stopped answering (serve.go): closed when the session ends, not parked
}

func newWarmTab(tab context.Context, closeTab func(), key string) *warmTab {
	w := &warmTab{ctx: tab, close: closeTab, key: key}
	chromedp.ListenTarget(tab, func(ev any) {
		f, ok := ev.(*page.EventScreencastFrame)
		if !ok {
			return
		}
		w.mu.Lock()
		on := w.onFrame
		w.mu.Unlock()
		if on != nil {
			on(f)
		}
	})
	return w
}

// markStuck keeps a tab that stopped answering from being parked and handed out again.
func (w *warmTab) markStuck() {
	w.mu.Lock()
	w.stuck = true
	w.mu.Unlock()
}

func (w *warmTab) watch(on func(*page.EventScreencastFrame)) {
	w.mu.Lock()
	w.onFrame = on
	w.mu.Unlock()
}

// warmPool is the parked tabs, by what they show to whom.
type warmPool struct {
	mu     sync.Mutex
	parked []*warmTab // oldest first
}

var warm = &warmPool{}

// take is the parked tab for key, thawed, or nil.
func (p *warmPool) take(key string) *warmTab {
	p.mu.Lock()
	var w *warmTab
	for i, t := range p.parked {
		if t.key == key {
			w = t
			p.parked = append(p.parked[:i], p.parked[i+1:]...)
			break
		}
	}
	p.mu.Unlock()
	if w == nil {
		return nil
	}
	w.expiry.Stop()
	// Thawed, and seen to be showing: a page still hidden after it sends no frames, and a new tab is
	// better than a screen that never changes again (#112).
	var shown string
	ctx, cancel := context.WithTimeout(w.ctx, tabAnswer)
	defer cancel()
	if err := chromedp.Run(ctx, page.SetWebLifecycleState(page.SetWebLifecycleStateStateActive),
		emulation.SetFocusEmulationEnabled(true),
		chromedp.Evaluate(`document.visibilityState`, &shown)); err != nil || shown != "visible" {
		slog.Warn("a parked dashboard would not show again; opening it afresh", "screen", key, "state", shown, "err", err)
		w.close()
		return nil
	}
	return w
}

// tabAnswer is as long as a tab is waited on for what should come at once: a thaw, a touch, a
// picture of what it shows. A tab that takes longer is stuck, and is not parked to be used again.
const tabAnswer = 10 * time.Second

// park stops a tab's stream, freezes it, and keeps it for warmFor.
func (p *warmPool) park(w *warmTab) {
	w.watch(nil)
	w.mu.Lock()
	stuck := w.stuck
	w.mu.Unlock()
	if stuck {
		w.close()
		return
	}
	ctx, cancel := context.WithTimeout(w.ctx, tabAnswer)
	defer cancel()
	if err := chromedp.Run(ctx, page.StopScreencast(),
		page.SetWebLifecycleState(page.SetWebLifecycleStateStateFrozen)); err != nil {
		w.close()
		return
	}
	p.mu.Lock()
	var evict *warmTab
	if len(p.parked) >= warmCount {
		evict, p.parked = p.parked[0], p.parked[1:]
	}
	p.parked = append(p.parked, w)
	w.expiry = time.AfterFunc(warmFor, func() { p.drop(w) })
	p.mu.Unlock()
	if evict != nil {
		evict.expiry.Stop()
		evict.close()
	}
}

// drop closes a parked tab whose time is up.
func (p *warmPool) drop(w *warmTab) {
	p.mu.Lock()
	for i, t := range p.parked {
		if t == w {
			p.parked = append(p.parked[:i], p.parked[i+1:]...)
			p.mu.Unlock()
			slog.Info("closing a parked dashboard nobody came back for", "screen", w.key)
			w.close()
			return
		}
	}
	p.mu.Unlock()
}
