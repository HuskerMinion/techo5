//go:build !linux

package kernel

import (
	"errors"
	"os"
)

func dropCache(*os.File) {}

func syncAll() {}

// PartitionDevice exists only on the device.
func PartitionDevice(name string) (string, error) {
	return "", errors.New("no " + name + " partition here")
}
