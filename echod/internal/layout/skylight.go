//go:build !dot && !spot && !panel

package layout

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
)

// The Skylight Calendar 15 has no idme. Its Wi-Fi MAC is no help either: the stock NVRAM its
// brcmfmac firmware loads gives every unit one placeholder address in Broadcom's 00:90:4c range.
// So the factory MAC is made from what is unique to the unit, its eMMC's CID: the first six
// bytes of a SHA-256 over it, marked locally administered and unicast. It never changes for a unit
// and two units never share it, which is what Home Assistant and the device name need.
const emmcCID = "/sys/class/block/mmcblk0/device/cid"

// skylightMAC is the unit's MAC, from its eMMC CID.
func skylightMAC() (string, bool) {
	b, err := os.ReadFile(emmcCID)
	if err != nil {
		return "", false
	}
	return macFromCID(string(b))
}

// macFromCID is the address a CID (32 hex digits, as the kernel prints it) gives.
func macFromCID(cid string) (string, bool) {
	cid = strings.ToLower(strings.TrimSpace(cid))
	if len(cid) != 32 || strings.Trim(cid, "0123456789abcdef") != "" {
		return "", false
	}
	h := sha256.Sum256([]byte("techo5-skylight-mac:" + cid))
	h[0] = h[0]&0xfc | 0x02 // locally administered, unicast
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", h[0], h[1], h[2], h[3], h[4], h[5]), true
}
