//go:build dot

package layout

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
)

// The Echo Dot 2 on Fire OS: echod lives under /system/app because that tree is labeled
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

	Manufacturer = "TECHO5"

	// LogPath is where techo5-run sends the daemon's output. The Dot's boot script logs to the
	// kernel ring rather than a file of its own, so BootLog is empty and a bundle reads dmesg for it.
	LogPath = "/data/techo5-linux/echod.log"
	BootLog = ""
)

var AnimationScripts = []string{StartAnimation, StopAnimation}

// Board, Model and DefaultName say which Echo this is. One build serves two: the Echo Dot 2nd gen
// (biscuit) and the Echo 2nd gen (radar) have the same SoC, the same sound card and codec, the same
// microphones, LED ring and buttons, and run the same rootfs, so they share the update channel too.
// What differs is the speaker behind the codec (hardware/speaker asks Radar) and the vendor tuning
// files, which this build reads only on the Dot. DefaultName is the fallback display name when a
// device has none recorded.
//
// The Echo Dot 3rd gen (donut) is built apart (-tags dot,donut): another SoC (MT8516), another sound
// card and four microphones in place of seven, so its capture and speaker files are its own, but
// everything that makes a speaker without a screen is the Dot's.
var Board, Model, DefaultName = boardInfo(detectBoard())

const (
	boardBiscuit = "biscuit"
	boardRadar   = "radar"
	boardDonut   = "donut"

	// deviceTypePath is the factory's product id, which Amazon's kernel serves from idme on Fire OS and
	// on the Linux image alike. It can end in a NUL.
	deviceTypePath = "/proc/idme/device_type_id"

	biscuitTypeID = "A3S5BH2HU6VAYF"
	radarTypeID   = "A7WXQPH584YP"
	donutTypeID   = "A32DOYMUN6DTXA"
)

// Radar is whether this is the Echo 2nd gen.
func Radar() bool { return Board == boardRadar }

// Donut is whether this is the Echo Dot 3rd gen.
func Donut() bool { return Board == boardDonut }

func boardInfo(board string) (string, string, string) {
	switch board {
	case boardRadar:
		return boardRadar, "Echo 2nd gen (radar)", "Echo"
	case boardDonut:
		return boardDonut, "Echo Dot 3 (donut)", "Echo Dot"
	}
	return boardBiscuit, "Echo Dot 2 (biscuit)", "Echo Dot"
}

// detectBoard reads the product id, and on Fire OS falls back to its ro.product.device where idme
// says nothing we know. A board neither names is run as a Dot, which is what this build was before it
// knew another, and BoardDoubt says so for boot.Run to log.
func detectBoard() string {
	// Amazon's kernels have /proc/idme; Linux 6.1 (the Echo 2nd gen's, from LibreEcho) has the same fields
	// only in the device tree the bootloader hands it, which readIDME reads too.
	typeID, err := readIDME("device_type_id", os.ReadFile)
	if board, ok := dotBoard(string(typeID), ""); ok {
		return board
	}
	product := ""
	if OnAndroid() {
		out, _ := exec.Command("/system/bin/getprop", "ro.product.device").Output()
		product = string(out)
	}
	board, ok := dotBoard(string(typeID), product)
	if !ok {
		BoardDoubt = boardDoubt(string(typeID), err, product, OnAndroid())
	}
	return board
}

// boardDoubt is BoardDoubt for a board neither idme nor Fire OS named. Without idme at all on the
// Linux image there is nothing to tell the boards apart by, and an Echo 2nd gen run as a Dot never
// lets go of its speaker's mute, so that one is said louder.
func boardDoubt(typeID string, readErr error, product string, android bool) string {
	if errors.Is(readErr, fs.ErrNotExist) && !android {
		return "there is no " + deviceTypePath + " on this image, so the board cannot be told: running it as an " +
			"Echo Dot 2 (biscuit), and an Echo 2nd gen (radar) will play nothing through its speaker"
	}
	return fmt.Sprintf("this Echo is not a board TECHO5 knows, running it as an Echo Dot 2 (biscuit): device_type_id %q, product %q",
		cleanID(typeID), cleanID(product))
}

// dotBoard names the board from idme's product id or, failing that, Fire OS's product name
// (biscuit_puffin, radar_puffin). ok is false where neither is one we know, and the board is then the
// Dot's.
func dotBoard(typeID, product string) (board string, ok bool) {
	switch cleanID(typeID) {
	case radarTypeID:
		return boardRadar, true
	case biscuitTypeID:
		return boardBiscuit, true
	case donutTypeID:
		return boardDonut, true
	}
	switch name, _, _ := strings.Cut(cleanID(product), "_"); name {
	case boardRadar:
		return boardRadar, true
	case boardBiscuit:
		return boardBiscuit, true
	case boardDonut:
		return boardDonut, true
	}
	return boardBiscuit, false
}

// cleanID is an id as read, without the NUL and the white space it can end in.
func cleanID(s string) string { return strings.Trim(s, "\x00 \t\r\n") }
