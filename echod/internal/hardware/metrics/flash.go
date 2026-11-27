package metrics

import (
	"path/filepath"
	"strconv"
	"strings"
)

// Flash is the eMMC the device boots from, by its name under /sys/block ("mmcblk0"), or empty where
// none is found. It is the mmcblk device whose card says it is an MMC: an SD card in a slot says SD,
// and the boot and rpmb areas of the eMMC have block devices of their own (mmcblk0boot0, mmcblk0rpmb)
// that are left out by name, since their counts are not the user area's. Looked for once, at start-up.
func (r Reader) Flash() string {
	devs, _ := filepath.Glob(r.path("sys/block/mmcblk*"))
	for _, dev := range devs {
		name := filepath.Base(dev)
		if _, err := strconv.Atoi(strings.TrimPrefix(name, "mmcblk")); err != nil {
			continue
		}
		if kind, err := text(dev + "/device/type"); err == nil && kind == "MMC" {
			return name
		}
	}
	return ""
}

// FlashLifeUsed is the chip's own estimate of how much of its rated life is used, in percent, from
// EXT_CSD DEVICE_LIFE_TIME_EST_TYP_A and _B, which the kernel prints in life_time as two hex values.
// Each counts in tenths of the rated life: 0x01 is 0 to 10 percent used, 0x0A is 90 to 100, and 0x0B
// is past it. The figure is the top of the range of the more worn of the two, so 0x03 reads 30 and
// 0x0B reads 110: one number Home Assistant can graph and alert on, where a range as text could only
// be shown. 0x00 is a chip older than eMMC 5.0, which keeps no estimate, and reads as nothing, as does
// a value past 0x0B, which the standard leaves undefined.
//
// The kernel reads EXT_CSD once, when it finds the card, so the file does not move while the device
// runs and once at start-up is all the reading it needs.
func (r Reader) FlashLifeUsed(dev string) Reading {
	if dev == "" {
		return Reading{}
	}
	body, err := text(r.path("sys/block/" + dev + "/device/life_time"))
	if err != nil {
		return Reading{}
	}
	fields := strings.Fields(body)
	if len(fields) != 2 {
		return Reading{}
	}
	var worst uint64
	for _, f := range fields {
		v, ok := hexByte(f)
		if !ok || v > 0x0B {
			return Reading{}
		}
		worst = max(worst, v)
	}
	if worst == 0 {
		return Reading{}
	}
	return known(float64(10 * worst))
}

// FlashSpareBlocks is the chip's pre-end-of-life status, EXT_CSD PRE_EOL_INFO, as the kernel prints
// it in pre_eol_info: 1 is normal, 2 is a warning (80 percent of the spare blocks used) and 3 is
// urgent (90 percent). 0 is a chip that does not say, or no file. The 6.18 kernel prints "0x01" and the
// 3.18, 4.4 and 4.9 kernels "01", so the prefix is optional. Read once, as life_time is.
func (r Reader) FlashSpareBlocks(dev string) int {
	if dev == "" {
		return 0
	}
	body, err := text(r.path("sys/block/" + dev + "/device/pre_eol_info"))
	if err != nil {
		return 0
	}
	v, ok := hexByte(body)
	if !ok || v > 3 {
		return 0
	}
	return int(v)
}

// FlashWritten is what has been written to the eMMC since boot, in bytes: field 7 of the device's
// stat, which counts 512-byte sectors whatever the chip's own sector size, written to any partition
// of its user area. The boot areas are devices of their own and are not in it, which leaves out only
// a kernel or bootloader update; everything the system writes as it runs goes to the user area.
func (r Reader) FlashWritten(dev string) Reading {
	if dev == "" {
		return Reading{}
	}
	body, err := text(r.path("sys/block/" + dev + "/stat"))
	if err != nil {
		return Reading{}
	}
	fields := strings.Fields(body)
	if len(fields) < 7 {
		return Reading{}
	}
	sectors, err := strconv.ParseUint(fields[6], 10, 64)
	if err != nil {
		return Reading{}
	}
	return known(float64(sectors) * 512)
}

// hexByte reads a value the mmc driver prints in hex, with or without its 0x.
func hexByte(s string) (uint64, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	v, err := strconv.ParseUint(s, 16, 8)
	return v, err == nil
}
