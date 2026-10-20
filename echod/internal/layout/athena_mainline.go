//go:build !dot && !spot && !panel

package layout

import (
	"bytes"
	"encoding/binary"
	"os"
	"strings"
)

// The 2nd gen Show 8 can also run a mainline 64-bit kernel of TECHO5's own (tools/linux/athena/kernel)
// instead of Amazon's 32-bit 4.4 one. The board is the same and the device tree keeps Amazon's root
// compatible, so Board still reads athena; what changes is every driver's name. AthenaMainline
// tells the two apart, from the kernel's release alone, so it holds from the first moment of boot.
var athenaMainline = Board == boardAthena && mainlineRelease(kernelRelease())

// AthenaMainline is whether this is the 2nd gen Show 8 on TECHO5's mainline kernel.
func AthenaMainline() bool { return athenaMainline }

// cronosMainline is the same question for the 2nd gen Show 5, whose vendor kernel is LineageOS's 4.9.
var cronosMainline = Board == boardCronos && mainlineRelease(kernelRelease())

// CronosMainline is whether this is the 2nd gen Show 5 on a mainline kernel.
func CronosMainline() bool { return cronosMainline }

// mainlineRelease: Amazon's athena kernel is 4.4 (4.4.146+ on the units read 2026-10-14); ours is
// 6.x. Anything that is not 4.x is taken as mainline.
func mainlineRelease(release string) bool {
	release = strings.TrimSpace(release)
	return release != "" && !strings.HasPrefix(release, "4.")
}

func kernelRelease() string {
	b, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	return string(b)
}

// idmeBootArea is where Amazon keeps idme on the eMMC: the second boot partition. Amazon's kernel
// serves it as /proc/idme; mainline has no idme driver, so it is read from here, read only.
const idmeBootArea = "/dev/mmcblk0boot1"

// idmeFallback reads one idme field from the eMMC where /proc/idme is missing. On the Skylight,
// which has no idme at all, only the MAC has a stand-in (skylight.go).
func idmeFallback(name string) (string, bool) {
	if Skylight() {
		if name == "mac_addr" {
			return skylightMAC()
		}
		return "", false
	}
	if !athenaMainline {
		return "", false
	}
	b, err := os.ReadFile(idmeBootArea)
	if err != nil {
		return "", false
	}
	return parseIdme(b, name)
}

// parseIdme reads Amazon's idme v2 layout: "beefdeed", a version string ("2.1"), the item count as
// a 32-bit little-endian word at 0x0c, then from 0x10 one item after another: the name in 16 bytes
// (NUL padded), its value's size, an export flag and the permission bits (32 bits each), then the
// value. Values are text, NUL padded. Read off the athena boot1 backups, 2026-10-15.
func parseIdme(b []byte, want string) (string, bool) {
	const (
		nameLen  = 16
		itemHead = nameLen + 12
	)
	if len(b) < 0x10 || !bytes.HasPrefix(b, []byte("beefdeed")) {
		return "", false
	}
	count := int(binary.LittleEndian.Uint32(b[0x0c:]))
	off := 0x10
	for i := 0; i < count && off+itemHead <= len(b); i++ {
		name := string(bytes.TrimRight(b[off:off+nameLen], "\x00"))
		size := int(binary.LittleEndian.Uint32(b[off+nameLen:]))
		val := off + itemHead
		if size < 0 || val+size > len(b) {
			return "", false
		}
		if name == want {
			return strings.TrimSpace(string(bytes.TrimRight(b[val:val+size], "\x00"))), true
		}
		off = val + size
	}
	return "", false
}
