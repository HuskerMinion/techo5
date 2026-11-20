//go:build !dot && !spot && !panel

package layout

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
)

// The Lenovo ThinkSmart View (CD-18781Y) is a Qualcomm APQ8053 desk display: an 800 by 1280 portrait
// panel in a 1280 by 800 landscape device, the Show 8's screen, with two digital microphones, a
// TAS5782M speaker amplifier, a light and proximity sensor, a camera with a lens cover and a
// microphone switch. It is not an Echo, but it is a screen device with the Show's kind of userland.
// It runs a 64-bit mainline kernel (msm8953-mainline, TECHO5's build in tools/linux/thinksmart/kernel)
// under lk2nd, so the board is read from the device tree, as the Skylight's is.
const (
	// thinkSmartCompatible is the root compatible of the mainline device tree
	// (arch/arm64/boot/dts/qcom/apq8053-lenovo-cd-18781y.dts): "lenovo,cd-18781y", "qcom,apq8053",
	// "qcom,msm8953" on the units read 2026-11-19.
	thinkSmartCompatible = "lenovo,cd-18781y"
)

// defaultName is the display name a device has when none is recorded.
func defaultName(board string) string {
	switch board {
	case boardSkylight:
		return "Skylight Calendar"
	case boardThinkSmart:
		return "ThinkSmart View"
	}
	return "Echo Show"
}

// ThinkSmart is whether this is the Lenovo ThinkSmart View (CD-18781Y).
func ThinkSmart() bool { return Board == boardThinkSmart }

// The ThinkSmart View has no idme. Its factory MAC is made from the eMMC's CID, as the Skylight's
// is, so a unit keeps one name in Home Assistant whatever address its Wi-Fi driver comes up with.
func init() {
	if Board != boardThinkSmart {
		return
	}
	boardIdme = func(name string) (string, bool) {
		if name != "mac_addr" {
			return "", false
		}
		b, err := os.ReadFile(emmcCID)
		if err != nil {
			return "", false
		}
		return thinkSmartMAC(string(b))
	}
}

// thinkSmartMAC is the address a CID (32 hex digits, as the kernel prints it) gives: the first six
// bytes of a SHA-256 over it, marked locally administered and unicast.
func thinkSmartMAC(cid string) (string, bool) {
	cid = strings.ToLower(strings.TrimSpace(cid))
	if len(cid) != 32 || strings.Trim(cid, "0123456789abcdef") != "" {
		return "", false
	}
	h := sha256.Sum256([]byte("techo5-thinksmart-mac:" + cid))
	h[0] = h[0]&0xfc | 0x02
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", h[0], h[1], h[2], h[3], h[4], h[5]), true
}
