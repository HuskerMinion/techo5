// Package maintenance is the housekeeping the device does to itself on a timer: the periodic work
// that keeps disk from filling, with no setting because there is no choice to make about it.
package maintenance

import (
	"context"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/android/logd"
	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/feature/recording"
)

// Period is how often the housekeeping runs. Not a setting: recordings are already pruned as each
// turn ends, so this is only the backstop for what a restart or a changed limit left behind, and
// nothing about the device changes fast enough for the interval to be worth a knob.
const Period = 5 * time.Minute

// The daemon's log is written through the shell that started it, so its lines sit in the page cache
// until something pushes them out: a device that loses power keeps whatever was flushed and nothing
// after it, which cost us the last minute before a Show was unplugged mid-fault (2026-09-19). A
// global sync every five seconds bounded that loss, but it made each log line a journal commit, and
// a Show 8 measured about 20 KB of flash written per line. eMMC wears with what is written to it.
// So only the log is flushed, with fdatasync: at once after a warning or an error (see urgentGap),
// which keeps those across a power cut, and on Period when it has grown, which keeps routine lines
// to within five minutes. Saved settings and the like sync themselves as they are written, and the
// root filesystem is read-only.

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

// Run sweeps once at start, so a restart clears its leftovers straight away, then on the period,
// and flushes the log on the period and after warnings.
func (m *Maintenance) Run(ctx context.Context) error {
	t := time.NewTicker(Period)
	defer t.Stop()

	log := newLogFlush(openStdLog())
	// again is when a warning that came too soon after the last flush is flushed; nil while none
	// is waiting.
	var again <-chan time.Time
	warned := func() {
		again = nil
		if wait := log.warned(time.Now()); wait > 0 {
			again = time.After(wait)
		}
	}

	for {
		recording.Get().Prune()

		for swept := false; !swept; {
			select {
			case <-ctx.Done():
				log.flush()
				return nil
			case <-logd.Warned():
				if again == nil {
					warned()
				}
			case <-again:
				warned()
			case <-t.C:
				log.flush()
				swept = true
			}
		}
	}
}
