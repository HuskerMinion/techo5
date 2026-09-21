//go:build !dot && !spot

package layout

import (
	"os"
	"strings"
)

// The Echo Show 5 2nd gen on LineageOS 18.1: nothing of Amazon's is taken over. The daemon is an
// init service of its own (tools/init/techo5.rc) with its state on /data. There are no vendor boot
// hooks to keep current, so AnimationScripts is empty and update.Ensure has nothing to write.
const (
	// On Android the daemon is /system/bin/techo5, which is what tools/init/techo5.rc runs and what
	// the updater replaces in place (remounting / rw, as the Dot does for /system). On the Linux image
	// it is /usr/local/bin/techo5 under busybox init (tools/linux/rootfs); see layout.Dir.
	AndroidDir = "/system/bin"
	BinaryName = "techo5"
	StateDir   = "/data/misc/techo5"

	Service     = AndroidDir + "/" + BinaryName
	ServiceName = "techo5"

	StockLabel = "u:object_r:system_file:s0"

	StartAnimation = ""
	StopAnimation  = ""

	FirewallHook = ""

	// LogTag is the daemon's logcat tag: `adb logcat -s techo5`.
	LogTag = "techo5"

	Manufacturer = "TECHO5"

	// DefaultName is the fallback display name when a device has none recorded.
	DefaultName = "Echo Show"

	// LogPath is where techo5-run sends the daemon's output, and BootLog what the boot script kept
	// of its own run. Both are what a diagnostics bundle reads.
	LogPath = "/data/techo5-linux/techo5.log"
	BootLog = "/run/boot.log"

)

var AnimationScripts = []string{}

// Board and Model say which Echo Show 5 this is. One build serves both generations: the 1st gen
// (checkers, 2019) runs the same kernel commit as the 2nd gen (cronos, 2021) on the same SoC, and
// the few parts that differ (the speaker codec, the mute driver, the camera sensor) ask Checkers at
// run time. techo5-checkers docs/hardware.md has what is known of the 1st gen.
var (
	Board = showBoard(kernelCmdline(), gatingDir)
	Model = map[string]string{
		boardCronos:   "Echo Show 5 2nd gen (cronos)",
		boardCheckers: "Echo Show 5 1st gen (checkers)",
	}[Board]
)

const (
	boardCronos   = "cronos"
	boardCheckers = "checkers"

	// gatingDir is the 1st gen's mute driver, amazon-gating; the 2nd gen has gpio-privacy instead.
	gatingDir = "/sys/devices/platform/amazon-gating"
)

// Checkers is whether this is the 1st gen Echo Show 5.
func Checkers() bool { return Board == boardCheckers }

// showBoard names the generation. The bootloader puts the board in the kernel command line, in the
// panel driver it names (lcm=1-st7701s_wsvga_dsi_vdo_checkers_st_kd_hsd on the 1st gen, ..._cronos_...
// on the 2nd). When the command line names neither, only the 1st gen has the amazon-gating driver.
func showBoard(cmdline, gating string) string {
	for _, f := range strings.Fields(cmdline) {
		if v, ok := strings.CutPrefix(f, "lcm="); ok {
			switch {
			case strings.Contains(v, boardCheckers):
				return boardCheckers
			case strings.Contains(v, boardCronos):
				return boardCronos
			}
		}
	}
	if _, err := os.Stat(gating); err == nil {
		return boardCheckers
	}
	return boardCronos
}

func kernelCmdline() string {
	b, _ := os.ReadFile("/proc/cmdline")
	return string(b)
}
