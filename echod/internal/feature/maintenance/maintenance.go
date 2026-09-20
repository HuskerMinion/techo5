// Package maintenance is the housekeeping the device does to itself on a timer: the periodic work
// that keeps disk from filling, with no setting because there is no choice to make about it.
package maintenance

import (
	"context"
	"sync"
	"syscall"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/feature/recording"
)

// Period is how often the housekeeping runs. Not a setting: recordings are already pruned as each
// turn ends, so this is only the backstop for what a restart or a changed limit left behind, and
// nothing about the device changes fast enough for the interval to be worth a knob.
const Period = 5 * time.Minute

// flushPeriod is how often what has been written is pushed out to flash. The daemon's log is
// written through the shell that started it, so its lines sit in the page cache until something
// syncs: a device that loses power keeps whatever was flushed and nothing after it, which cost us
// the last minute before a Show was unplugged mid-fault (2026-09-19). Five seconds bounds that
// loss; a sync with nothing dirty costs nothing, and the root filesystem is read-only anyway.
const flushPeriod = 5 * time.Second

func init() {
	component.Register(component.Device, Get(), component.Order(80))
}

var (
	once   sync.Once
	shared *Maintenance
)

type Maintenance struct{}

func Get() *Maintenance {
	once.Do(func() { shared = &Maintenance{} })
	return shared
}

func (m *Maintenance) Name() string { return "maintenance" }

// Run sweeps once at start, so a restart clears its leftovers straight away, then on the period.
func (m *Maintenance) Run(ctx context.Context) error {
	t := time.NewTicker(Period)
	defer t.Stop()

	flush := time.NewTicker(flushPeriod)
	defer flush.Stop()

	for {
		recording.Get().Prune()

		for swept := false; !swept; {
			select {
			case <-ctx.Done():
				return nil
			case <-flush.C:
				syscall.Sync()
			case <-t.C:
				swept = true
			}
		}
	}
}
