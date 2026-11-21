package main

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// Loading a dashboard is the dear part: on a Raspberry Pi a heavy one keeps Chrome busy for tens of
// seconds. So a screen that goes away does not take its tab with it at once. The tab is parked -
// its stream stopped, the page frozen so its scripts stop too - and a screen that comes back for
// the same dashboard within warmFor gets it back, thawed, without loading anything. A device
// restarting, a dashboard switched away from and back, the connection dropping for a moment: none
// of them is a reload.

// warmFor and warmCount are set once at start, from DASHCAST_WARM_FOR and DASHCAST_WARM_COUNT
// (warmSettings), before any screen connects, and only read after that.
var (
	warmFor   = warmForDefault
	warmCount = warmCountDefault // tabs parked at once, the oldest closed to make room
)

const (
	warmForDefault   = 5 * time.Minute
	warmCountDefault = 4

	// warmCountMax is the most tabs DASHCAST_WARM_COUNT may ask to keep. A frozen tab still holds
	// its page's memory, a couple of hundred megabytes for a dashboard, and a count typed with one
	// zero too many would let Chrome take all of a small machine's memory.
	warmCountMax = 16
)

// warmSettings reads how long a parked tab is kept and how many are kept, from the values of
// DASHCAST_WARM_FOR and DASHCAST_WARM_COUNT. Unset is the default; a value that cannot be used is
// logged and the default kept, since a dashboard that loads more slowly is better than no dashcast
// at all.
func warmSettings(forS, countS string) (time.Duration, int) {
	d, n := warmForDefault, warmCountDefault
	if forS = strings.TrimSpace(forS); forS != "" {
		v, err := time.ParseDuration(forS)
		if err != nil || v <= 0 {
			slog.Warn("DASHCAST_WARM_FOR is not a time like 30m or 12h; keeping the default",
				"value", forS, "default", warmForDefault)
		} else {
			d = v
		}
	}
	if countS = strings.TrimSpace(countS); countS != "" {
		v, err := strconv.Atoi(countS)
		if err != nil || v <= 0 || v > warmCountMax {
			slog.Warn(fmt.Sprintf("DASHCAST_WARM_COUNT is a number from 1 to %d; keeping the default", warmCountMax),
				"value", countS, "default", warmCountDefault)
		} else {
			n = v
		}
	}
	return d, n
}

// warmTab is a tab and whoever is watching its frames: the session using it, or nobody while it is
// parked. A tab's frames arrive through one listener for its whole life, since a listener cannot be
// taken off again.
type warmTab struct {
	ctx   context.Context
	close func()

	mu      sync.Mutex
	onFrame func(*page.EventScreencastFrame)
	onField func(field) // a field got or lost focus (field.go)
	key     string
	expiry  *time.Timer
	stuck   bool // it stopped answering (serve.go): closed when the session ends, not parked
}

func newWarmTab(tab context.Context, closeTab func(), key string) *warmTab {
	w := &warmTab{ctx: tab, close: closeTab, key: key}
	chromedp.ListenTarget(tab, func(ev any) {
		switch e := ev.(type) {
		case *page.EventScreencastFrame:
			w.mu.Lock()
			on := w.onFrame
			w.mu.Unlock()
			if on != nil {
				on(e)
			}
		case *runtime.EventBindingCalled:
			if e.Name != fieldBinding {
				return
			}
			f, ok := parseField(e.Payload)
			w.mu.Lock()
			on := w.onField
			w.mu.Unlock()
			if ok && on != nil {
				on(f)
			}
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

// watchField has on told when a field in the tab gets or loses focus; nil for nobody.
func (w *warmTab) watchField(on func(field)) {
	w.mu.Lock()
	w.onField = on
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
	w.watchField(nil)
	w.mu.Lock()
	stuck := w.stuck
	w.mu.Unlock()
	if stuck {
		w.close()
		return
	}
	// A field left with focus would not take it again when tapped on the screen's return, and the
	// keyboard would not come up for it.
	blur(w.ctx)
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
