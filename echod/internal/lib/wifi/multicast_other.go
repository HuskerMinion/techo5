//go:build !linux

package wifi

import (
	"context"
	"errors"
)

// RefreshMulticast is Linux only.
func RefreshMulticast(context.Context) error { return errors.New("not on this system") }
