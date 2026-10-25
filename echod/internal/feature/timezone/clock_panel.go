//go:build panel

package timezone

import (
	"errors"
	"time"
)

// Android keeps a tablet's clock; an app cannot set it.
func ntpSynced() bool { return true }

func setSystemClock(time.Time) error { return errors.New("the clock is Android's") }
