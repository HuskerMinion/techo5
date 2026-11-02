//go:build linux && !dot && !spot

// The boost: schedutil follows the load, and a swipe through the menus is a load that arrives all at
// once, so the first frames are drawn on clocks that are still climbing. While a finger is on the screen,
// and for a moment after, the minimum frequency of each cluster is raised to its maximum, which is the
// performance governor for as long as it is wanted and the board's own governor the rest of the time.
//
// The same moments also move the daemon onto the fastest cores. The scheduler puts the interface's work
// on the slow cluster even with every clock at its maximum, and a swipe is drawn quicker on the fast
// one. The daemon sits in a cpuset that spans every core and is narrowed to the fast ones only while
// a finger is down and for the hold after; what it starts (a video player among them) is in the set
// too, so it has every core back when the hold ends and none of the time in between is spent narrowed.
package cpuboost

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/touch"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

const (
	cpufreqDir = "/sys/devices/system/cpu/cpufreq"

	// hold is how long the clocks stay up after the last finger lifts: a second swipe usually follows a
	// first, and a pause between them is no reason to drop and climb again.
	hold = 4 * time.Second
)

func init() {
	// Only the 2nd gen Show 8 on TECHO5's mainline kernel, where it was measured: the 2nd gen Show 5
	// pays I2C and a voltage change for every frequency step (boot.sh), and the other boards' kernels
	// have no cpufreq policies to move.
	if layout.AthenaMainline() {
		component.Register(component.Hardware, &Boost{}, component.Order(91))
	}
}

// source is the screen as the boost sees it.
type source interface {
	// Touched is whether a finger is on the panel now.
	Touched() bool
	// Listen calls fn as fingers land and lift, and returns the function that stops it.
	Listen(fn func(down bool)) (cancel func())
}

type screen struct{ s *touch.Screen }

func (c screen) Touched() bool                        { return c.s.Touched() }
func (c screen) Listen(fn func(bool)) (cancel func()) { return c.s.Down.Listen(fn) }

// Boost raises the clocks on touch.
type Boost struct{}

func (b *Boost) Name() string { return "cpuboost" }

func (b *Boost) Run(ctx context.Context) error {
	return run(ctx, "/", screen{touch.Get()}, hold)
}

// policy is one cluster's frequency files, and the minimum it is put back to.
type policy struct {
	min, max string // paths: scaling_min_freq, cpuinfo_max_freq
	floor    string // the minimum to restore, as the kernel writes it
	raised   bool
}

// policies finds the clusters under root, each with the minimum to put back: the one it has now, unless
// a daemon that died while boosted left it at the maximum, when it is the hardware's lowest.
func policies(root string) []*policy {
	dirs, _ := filepath.Glob(filepath.Join(root, cpufreqDir, "policy*"))
	var out []*policy
	for _, d := range dirs {
		p := &policy{min: filepath.Join(d, "scaling_min_freq"), max: filepath.Join(d, "cpuinfo_max_freq")}
		cur, errC := read(p.min)
		top, errT := read(p.max)
		if errC != nil || errT != nil {
			continue
		}
		p.floor = cur
		if cur == top {
			if lowest, err := read(filepath.Join(d, "cpuinfo_min_freq")); err == nil {
				p.floor = lowest
			}
		}
		out = append(out, p)
	}
	return out
}

func read(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(b))
	if _, err := strconv.Atoi(v); err != nil {
		return "", err
	}
	return v, nil
}

var warned sync.Once

func write(path, v string) {
	if err := os.WriteFile(path, []byte(v), 0o644); err != nil {
		warned.Do(func() { slog.Warn("cpuboost: a cluster's minimum frequency", "path", path, "err", err) })
	}
}

func raise(ps []*policy) {
	for _, p := range ps {
		if p.raised {
			continue
		}
		if top, err := read(p.max); err == nil {
			write(p.min, top)
			p.raised = true
		}
	}
}

func lower(ps []*policy) {
	for _, p := range ps {
		if p.raised {
			write(p.min, p.floor)
			p.raised = false
		}
	}
}

// run holds the clusters at their maximum while src has a finger down and for hold after, and puts
// the minimums back when it returns.
func run(ctx context.Context, root string, src source, hold time.Duration) error {
	ps := policies(root)
	if len(ps) == 0 {
		return nil
	}
	// Starting is not boosted: a minimum a daemon that died left at the maximum goes back now.
	for _, p := range ps {
		if cur, err := read(p.min); err == nil && cur != p.floor {
			write(p.min, p.floor)
		}
	}
	defer lower(ps)

	cs := newCpuset(root)
	defer cs.close()

	// A listener must not block, so it only wakes the loop; what the screen says is read there.
	wake := make(chan struct{}, 1)
	cancel := src.Listen(func(bool) {
		select {
		case wake <- struct{}{}:
		default:
		}
	})
	defer cancel()

	active := src.Touched

	idle := time.NewTimer(hold)
	if !idle.Stop() {
		<-idle.C
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-wake:
			// A finger that landed and lifted between two looks is still a touch: a tap changes the
			// screen, and that is drawn on the raised clocks too.
			raise(ps)
			cs.narrow()
			idle.Stop()
			select {
			case <-idle.C:
			default:
			}
			if !active() {
				idle.Reset(hold)
			}
		case <-idle.C:
			if !active() {
				lower(ps)
				cs.widen()
			}
		}
	}
}
