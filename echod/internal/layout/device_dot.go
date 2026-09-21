//go:build dot

package layout

// The Echo Dot 2 on Fire OS: echod lives under /system/app because that tree is labelled
// system_file, which keeps an init-started service in init's own domain, and it runs as Amazon's
// ledcontroller service, which removes the only other writer of the LED ring and gets init's
// supervision. /system is read-only once installed, so anything written after install goes on /data.
const (
	AndroidDir = "/system/app/echod"
	BinaryName = "echod"
	StateDir   = "/data/misc/echolocal"

	Service     = "/system/bin/ledcontroller"
	ServiceName = "ledcontroller"

	// StockLabel is what the displaced vendor binary carried.
	StockLabel = "u:object_r:ledd_exec:s0"

	// The boot animation wrappers init runs; both call ledctrl, which waits on a binder service echod
	// does not publish. StartAnimation runs on the way up and StopAnimation once Android reports the
	// boot finished, which is the difference that decides what may go in them.
	StartAnimation = "/system/bin/start_animation.sh"
	StopAnimation  = "/system/bin/stop_animation.sh"

	// FirewallHook is a script Amazon's firewall.sh runs if it exists, after building its allowlist
	// and after flushing INPUT. Occupying it is how our port stays open across every invocation
	// without editing their script. Greengrass is not installed and nothing else references it.
	FirewallHook = "/system/bin/greengrass_firewall.sh"

	// LogTag is echod's logcat tag: `adb logcat -s echolocal`.
	LogTag = "echolocal"

	Manufacturer = "EchoLocal"
	Model        = "Echo Dot 2 (biscuit)"
	Board        = "biscuit"

	// DefaultName is the fallback display name when a device has none recorded.
	DefaultName = "Echo Dot"

	// LogPath is where techo5-run sends the daemon's output. The Dot's boot script logs to the
	// kernel ring rather than a file of its own, so BootLog is empty and a bundle reads dmesg for it.
	LogPath = "/data/techo5-linux/echod.log"
	BootLog = ""

)

var AnimationScripts = []string{StartAnimation, StopAnimation}
