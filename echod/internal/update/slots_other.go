//go:build !linux

package update

import (
	"context"
	"errors"
)

func slotSystem() bool { return false }

func installRootfs(context.Context, Manifest, func(float32), func(string)) error {
	return errors.New("update: slot installs only work on the device")
}

func rebootIntoSlot() error {
	return errors.New("update: slot reboots only work on the device")
}
