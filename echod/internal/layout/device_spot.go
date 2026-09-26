//go:build spot

package layout

// The Echo Spot (2017) on LineageOS 18.1, laid out as the Echo Show 5 is: the daemon is an init
// service of its own with its state on /data, and nothing of Amazon's is taken over.
const (
	// On Android the daemon is /system/bin/techo5; on the Linux image it is /usr/local/bin/techo5
	// under busybox init. See layout.Dir.
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
	Model        = "Echo Spot (rook)"
	Board        = "rook"

	// DefaultName is the fallback display name when a device has none recorded.
	DefaultName = "Echo Spot"

	// LogPath is where the init script sends the daemon's output, and BootLog what the boot script
	// kept of its own run.
	LogPath = "/data/techo5-linux/techo5.log"
	BootLog = "/run/boot.log"
)

var AnimationScripts = []string{}
