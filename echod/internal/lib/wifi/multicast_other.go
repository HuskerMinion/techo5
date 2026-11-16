//go:build !linux

package wifi

import (
	"context"
	"errors"
)

// RefreshMulticast is Linux only.
func RefreshMulticast(context.Context) error { return errors.New("not on this system") }

// RefreshRxFilter is Linux only.
func RefreshRxFilter(context.Context) error { return errors.New("not on this system") }
