package addons

import "time"

// clock is the time as the supervisor reads it: a restart's backoff, an update's minute on trial, and
// how long a run lasted. An interface, so a test can move it on by hand rather than wait for it.
type clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
