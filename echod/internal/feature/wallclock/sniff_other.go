//go:build !linux

package wallclock

import (
	"context"
	"errors"
)

// sniffClock has no meaning off Linux: the Bluetooth stack this feature uses is Linux's.
func sniffClock(context.Context, string) error {
	return errors.New("Bluetooth sniff mode is Linux only")
}
