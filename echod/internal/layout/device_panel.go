//go:build panel

package layout

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// TECHO5 Panel: an Android tablet or a Meta Portal, with echod running inside an ordinary app rather
// than as an init service. Nothing is rooted, so everything the daemon keeps lives in the app's own
// files directory, and the app hands it the microphone over a local socket (see mic/capture_panel.go).
const (
	// PackageName is the Android app echod runs inside. Its files directory is the only place the
	// daemon may write.
	PackageName = "app.techo5.panel"

	BinaryName  = "techo5"
	ServiceName = "techo5"

	StockLabel = "u:object_r:system_file:s0"

	StartAnimation = ""
	StopAnimation  = ""

	FirewallHook = ""

	// LogTag is the daemon's logcat tag: `adb logcat -s techo5`.
	LogTag = "techo5"

	Manufacturer = "TECHO5"

	// DefaultName is the fallback display name when a device has none recorded.
	DefaultName = "TECHO5 Panel"
)

// StateDir is the app's files directory, which the app starts echod with as HOME and as its working
// directory. It is found rather than compiled in: Android gives each user of the tablet the app's own
// directory (/data/user/<user>/app.techo5.panel/files), so a panel set up for a second user is not
// in user 0's. The first user's is the fallback, for a daemon started by hand.
var StateDir = stateDir()

var (
	// AndroidDir is where an in-place update would put the binary. The app ships the daemon in its
	// own APK and updates come with the app, so nothing is ever written here.
	AndroidDir = StateDir + "/bin"
	Service    = AndroidDir + "/" + BinaryName

	LogPath = StateDir + "/techo5.log"
	BootLog = StateDir + "/boot.log"
)

func stateDir() string {
	for _, d := range []string{os.Getenv("HOME"), workingDir()} {
		if filepath.IsAbs(d) && strings.HasSuffix(filepath.Clean(d), "/"+PackageName+"/files") {
			return filepath.Clean(d)
		}
	}
	return "/data/user/0/" + PackageName + "/files"
}

func workingDir() string {
	d, _ := os.Getwd()
	return d
}

var AnimationScripts = []string{}

// Board is the Android device name and Model what the tablet calls itself, read once at start from
// the system properties an app may read.
var (
	Board = getprop("ro.product.device", "android")
	Model = "TECHO5 Panel (" + getprop("ro.product.model", "Android") + ")"
)

// ZoneDB is Android's packed zone database ("tzdata"): the APEX copy that system updates keep
// current on Android 10 and later, an update dropped in data on older ones, or the one the system
// image shipped with. Empty when there is none.
var ZoneDB = firstFile(
	"/apex/com.android.tzdata/etc/tz/tzdata",
	"/data/misc/zoneinfo/current/tzdata",
	"/system/usr/share/zoneinfo/tzdata",
)

func firstFile(paths ...string) string {
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// The zone Home Assistant gave, which feature/timezone keeps at StateDir/localtime. On a Linux image
// /etc/localtime links to that file and the restart after a change picks it up; on Android, Go never
// reads a local zone at all (time.Local is always UTC there), so it is loaded here, in the package
// everything else imports first, before anything has read the clock.
//
// ZONEINFO is pointed at Android's zone database first, for the zones named elsewhere (a calendar's
// TZID): Go only knows the places older Androids kept it, and Android 10 and later keep it in an APEX.
func init() {
	if ZoneDB != "" && os.Getenv("ZONEINFO") == "" {
		os.Setenv("ZONEINFO", ZoneDB)
	}
	b, err := os.ReadFile(StateDir + "/localtime")
	if err != nil {
		return
	}
	if loc, err := time.LoadLocationFromTZData("Local", b); err == nil {
		time.Local = loc
	}
}

// Checkers and Crown are Echo Show boards; a panel is neither.
func Checkers() bool { return false }

func Crown() bool { return false }

// Show8 is false: a panel's canvas is whatever size the app asked for (hardware/screen), and the
// display tells home that size once the screen has opened (home.SetPanelSize), so camera frames,
// photos, the now-playing picture and the rain map are made at it rather than at a board's.
func Show8() bool { return false }

// Athena is false: a panel is not the 2nd gen Show 8, on either kernel, so main's athena paths
// (the front camera, Bluetooth's athena handling) never run in the app.
func Athena() bool { return false }

func getprop(name, fallback string) string {
	out, err := exec.Command("/system/bin/getprop", name).Output()
	if v := strings.TrimSpace(string(out)); err == nil && v != "" {
		return v
	}
	return fallback
}
