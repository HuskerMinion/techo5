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

	// sweepEvery is how often, while the daemon is narrowed, what it has started since is moved out of
	// its group (cpuset.go).
	sweepEvery = 500 * time.Millisecond

	// markerPath records the minimums that are raised, so the next daemon can put back what a daemon
	// that died while boosted left. It is under /run: a reboot resets the minimums too.
	markerPath = "run/techo5/cpuboost-raised"
)

// retryAfter is how long after a minimum would not go back that it is tried again; a variable for the tests.
var retryAfter = time.Second

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
	floor    string // the minimum as it was when the boost raised it, as the kernel writes it
	raised   bool
}

// policies finds the clusters under root.
func policies(root string) []*policy {
	dirs, _ := filepath.Glob(filepath.Join(root, cpufreqDir, "policy*"))
	var out []*policy
	for _, d := range dirs {
		p := &policy{min: filepath.Join(d, "scaling_min_freq"), max: filepath.Join(d, "cpuinfo_max_freq")}
		if _, err := read(p.min); err != nil {
			continue
		}
		if _, err := read(p.max); err != nil {
			continue
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

// lastWarned is the failure last logged: a write that keeps failing says so once, not at every touch.
var (
	warnMu     sync.Mutex
	lastWarned string
)

// warn says a write failed, once for as long as the message stays the same.
func warn(path string, err error) {
	warnMu.Lock()
	defer warnMu.Unlock()
	if msg := path + ": " + err.Error(); msg != lastWarned {
		lastWarned = msg
		slog.Warn("cpuboost: a write", "path", path, "err", err)
	}
}

func write(path, v string) error {
	err := os.WriteFile(path, []byte(v), 0o644)
	if err != nil {
		warn(path, err)
	}
	return err
}

// raise puts every cluster's minimum at its maximum, remembering the minimum it replaced. One that
// would not take it is not counted as raised.
func raise(root string, ps []*policy) {
	changed := false
	for _, p := range ps {
		if p.raised {
			continue
		}
		cur, errC := read(p.min)
		top, errT := read(p.max)
		if errC != nil || errT != nil || write(p.min, top) != nil {
			continue
		}
		p.floor, p.raised = cur, true
		changed = true
	}
	if changed {
		saveMarker(root, ps)
	}
}

// lower puts back the minimums that were raised, and says whether every one went back: one that did not
// stays raised, to be tried again.
func lower(root string, ps []*policy) bool {
	done := true
	for _, p := range ps {
		if !p.raised {
			continue
		}
		if write(p.min, p.floor) != nil {
			done = false
			continue
		}
		p.raised = false
	}
	if done {
		_ = os.Remove(filepath.Join(root, markerPath))
	} else {
		saveMarker(root, ps)
	}
	return done
}

// saveMarker writes the raised minimums as lines of path and the value to put back.
func saveMarker(root string, ps []*policy) {
	var b strings.Builder
	for _, p := range ps {
		if p.raised {
			b.WriteString(p.min + " " + p.floor + "\n")
		}
	}
	path := filepath.Join(root, markerPath)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(b.String()), 0o644)
}

// repair puts back the minimums a daemon that died while boosted left raised, as its marker lists them,
// each only if it is still at its maximum: anything else was set by somebody since. With no marker nothing
// is touched, so a minimum that somebody set to the maximum on purpose stays.
func repair(root string) {
	path := filepath.Join(root, markerPath)
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		cur, errC := read(f[0])
		top, errT := read(filepath.Join(filepath.Dir(f[0]), "cpuinfo_max_freq"))
		if errC == nil && errT == nil && cur == top {
			_ = write(f[0], f[1])
		}
	}
	_ = os.Remove(path)
}

// run holds the clusters at their maximum while src has a finger down and for after, and puts the
// minimums back when it returns.
func run(ctx context.Context, root string, src source, after time.Duration) error {
	ps := policies(root)
	if len(ps) == 0 {
		return nil
	}
	repair(root)
	defer lower(root, ps)

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

	// While narrowed, a ticker moves out of the group what the daemon has started since (a video
	// player, AirPlay): they are not the interface and keep every core.
	var ticker *time.Ticker
	var sweepC <-chan time.Time
	stopSweep := func() {
		if ticker != nil {
			ticker.Stop()
			ticker, sweepC = nil, nil
		}
	}
	defer stopSweep()

	// release gives the cores and the clocks back, as the end of the hold does.
	release := func(idle *time.Timer) {
		cs.widen()
		stopSweep()
		if !lower(root, ps) {
			idle.Reset(retryAfter)
		}
	}

	for len(kick) > 0 {
		<-kick
	}
	idle := time.NewTimer(after)
	if !idle.Stop() {
		<-idle.C
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-kick:
			// A switch moved. Off gives everything back now; on waits for the next touch. Fast cores
			// off gives the cores back and keeps the clocks to the end of the hold.
			if !Enabled() {
				idle.Stop()
				select {
				case <-idle.C:
				default:
				}
				release(idle)
			} else if !FastCores() {
				cs.widen()
				stopSweep()
			}
		case <-wake:
			if !Enabled() {
				continue
			}
			// A finger that landed and lifted between two looks is still a touch: a tap changes the
			// screen, and that is drawn on the raised clocks too.
			raise(root, ps)
			if FastCores() {
				if cs != nil && ticker == nil {
					ticker = time.NewTicker(sweepEvery)
					sweepC = ticker.C
				}
				cs.narrow()
			}
			idle.Stop()
			select {
			case <-idle.C:
			default:
			}
			if !src.Touched() {
				idle.Reset(after)
			}
		case <-sweepC:
			cs.sweep()
		case <-idle.C:
			if src.Touched() {
				continue
			}
			release(idle)
		}
	}
}
