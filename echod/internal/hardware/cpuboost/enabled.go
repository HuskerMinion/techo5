package cpuboost

import "sync/atomic"

var (
	// off is whether the boost is switched off (the Touch boost switch, feature/diag); on is how it comes.
	off atomic.Bool

	// slow is whether the fast cores part is switched off (Touch boost: fast cores). It matters only while
	// the boost is on; on is how it comes.
	slow atomic.Bool

	// kick tells the boost the switch moved. It holds one token: the boost reads the switch when it wakes.
	kick = make(chan struct{}, 1)
)

// SetEnabled turns the boost on or off. Off, the clocks and cores are given back at once and a touch
// changes neither. Where there is no boost (every board but the 2nd gen Show 8 on its mainline kernel)
// it does nothing.
func SetEnabled(on bool) {
	off.Store(!on)
	select {
	case kick <- struct{}{}:
	default:
	}
}

// Enabled is whether the boost is switched on.
func Enabled() bool { return !off.Load() }

// SetFastCores turns the preference for the fast cores on or off, with the boost's clocks as they are.
// Off, the daemon is given every core back at once and a touch no longer narrows it.
func SetFastCores(on bool) {
	slow.Store(!on)
	select {
	case kick <- struct{}{}:
	default:
	}
}

// FastCores is whether a touch also moves the daemon onto the fast cores.
func FastCores() bool { return !slow.Load() }
