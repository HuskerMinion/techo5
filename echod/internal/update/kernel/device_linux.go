//go:build linux

package kernel

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// dropCache makes the next read of f come from the device, not the page cache: a block device's
// buffers are flushed and dropped (BLKFLSBUF), a plain file's pages are let go (tests).
func dropCache(f *os.File) {
	fd := int(f.Fd())
	if err := unix.IoctlSetInt(fd, unix.BLKFLSBUF, 0); err != nil {
		_ = unix.Fadvise(fd, 0, 0, unix.FADV_DONTNEED)
	}
}

// syncAll flushes every filesystem, the data partition's journal with the rest.
func syncAll() { unix.Sync() }

// PartitionDevice is the eMMC partition with this name: boot is mmcblk0p20 on athena's mainline
// kernel, recovery mmcblk0p8 on the Skylight.
func PartitionDevice(name string) (string, error) {
	ms, _ := filepath.Glob("/sys/class/block/mmcblk*p*/uevent")
	for _, m := range ms {
		f, err := os.Open(m)
		if err != nil {
			continue
		}
		s := bufio.NewScanner(f)
		for s.Scan() {
			if strings.TrimSpace(s.Text()) == "PARTNAME="+name {
				f.Close()
				return "/dev/" + filepath.Base(filepath.Dir(m)), nil
			}
		}
		f.Close()
	}
	return "", errors.New("no eMMC partition named " + name)
}
