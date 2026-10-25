// Package layout is the on-device layout echoctl writes and echod reads. Both binaries have to
// agree on every name here, so they come from one place rather than being repeated.
package layout

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Where echod and its state live. AndroidDir and StateDir are the device's own (device_dot.go,
// device_cronos.go); everything else hangs off them.
//
// Dir is settled at start rather than compiled in: on Android it is AndroidDir, the path the init
// service runs, but the same binary also runs on the Linux image (tools/linux), where there is no
// /system and the daemon lives wherever its supervisor started it. There it is the directory of the
// running executable, so the in-place updater and its trial still replace the right file.
var (
	Dir    = installDir()
	Binary = Dir + "/" + BinaryName

	// PrevBinary is the binary an update replaced, kept until the new one has proved itself. Its
	// presence at boot is what says a trial never finished, so nothing may leave one lying around.
	// OldBinary is where a proven update files it, one generation back.
	PrevBinary = Binary + ".prev"
	OldBinary  = Binary + ".old"
)

// BoardDoubt says why Board is a guess, empty where it is known (and off the Dot's build, where there
// is only one board). The board is found as the package loads, before the daemon's logger is set up,
// so boot.Run logs this rather than detection itself.
var BoardDoubt string

// Variables rather than constants, as everything under StateDir is: a panel finds its StateDir when
// it starts (device_panel.go).
var (
	KeyPath  = StateDir + "/psk"
	NamePath = StateDir + "/name"

	// UpdatingPath holds the version being tried, so a rollback can say which one it took out. It is
	// under /data because the boot hook reads it after a restore has already remounted /system back to
	// read-only.
	UpdatingPath = StateDir + "/updating"

	// LinuxDir is where the Linux image installs the daemon, and the fallback when the executable's
	// own path cannot be read.
	LinuxDir = "/usr/local/bin"

	// setprop is what marks an Android userspace: the property service is the one thing every Android
	// has and no plain Linux does.
	setprop = "/system/bin/setprop"
)

func installDir() string {
	if OnAndroid() {
		return AndroidDir
	}
	exe, err := os.Executable()
	if err != nil {
		return LinuxDir
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	dir := filepath.Dir(exe)
	if dir == "." || dir == "" {
		return LinuxDir
	}
	return dir
}

// BackupSuffix marks the vendor binary an install displaced, and Backup is where it is kept.
const BackupSuffix = ".orig"

var Backup = Service + BackupSuffix

// OurLabel is the SELinux label echod's files carry. A service's domain comes from the label of the
// file init execs, and system_file has no transition rule, which leaves echod in init's own domain.
const OurLabel = "u:object_r:system_file:s0"

// Properties echod publishes about itself. Started is an uptime, which only moves forward
// within a boot, so a changed value means a new process.
const (
	StartedProp = "echolocal.started"
	StateProp   = "echolocal.state"

	// TrialProp marks that a process this boot already took an update and has not committed it.
	// Deliberately not a persist property: it has to survive init restarting echod, which is what
	// makes a second attempt recognizable, and it has to be forgotten across a reboot, which is what
	// gives the boot hook its turn.
	TrialProp = "echolocal.trial"

	// RolledBackProp is set by the boot hook when it puts the previous binary back, so the failure
	// reaches Home Assistant instead of only logcat.
	RolledBackProp = "echolocal.rolledback"
)

// Port is the ESPHome native API port Home Assistant expects.
const Port = 6053

// MaxNodeName is the length limit the ESPHome API imposes on a node name.
const MaxNodeName = 31

// Platform is what the optional EchoLocal Home Assistant integration keys on, so it is kept across
// devices; Manufacturer, Model, Board and DefaultName are the device's own.
const Platform = "echolocal"

// MACPath is the address the factory recorded, which the Wi-Fi driver takes when it comes up. idme
// is a kernel interface, so it reads this early in boot, before wlan0 exists and without /data.
const MACPath = "/proc/idme/mac_addr"

// StatePath holds echod's runtime settings.
var StatePath = StateDir + "/state.json"

// Wake word models live in /data, not /system: Home Assistant can offer new ones at runtime and
// /system is mounted read-only.
var ModelDir = StateDir + "/models"

// RecordingDir holds the kept turn audio, one WAV and one metadata file per turn, named by turn id.
// A variable rather than a constant so that a test can keep recordings somewhere harmless.
var RecordingDir = StateDir + "/recordings"

// WakeClipDir holds the kept wake word clips, a folder for each kind (feature/detect, clips.go).
var WakeClipDir = StateDir + "/wake-clips"

// MAC normalizes an address into the form Home Assistant compares against, and reports "" for
// anything that would not identify a device. idme writes twelve hex digits with no separators.
func MAC(raw string) string {
	var digits strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'f' {
			digits.WriteRune(r)
		}
	}

	s := digits.String()
	if len(s) != 12 || s == "000000000000" {
		return ""
	}

	var mac strings.Builder
	for i := 0; i < len(s); i += 2 {
		if i > 0 {
			mac.WriteByte(':')
		}
		mac.WriteString(s[i : i+2])
	}
	return mac.String()
}

// FactoryMAC reads and normalizes the immutable address recorded for the Wi-Fi device.
func FactoryMAC() (string, error) {
	raw, err := readMAC()
	if err != nil {
		v, ok := idmeFallback("mac_addr")
		if !ok {
			return "", fmt.Errorf("reading the factory MAC: %w", err)
		}
		raw = []byte(v)
	}
	mac := MAC(string(raw))
	if mac == "" {
		return "", fmt.Errorf("%s holds %q, which is not an address", MACPath, strings.TrimSpace(string(raw)))
	}
	return mac, nil
}

// NameFromMAC builds the fallback display name, unique per device.
func NameFromMAC(mac string) string {
	var hex strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(mac)) {
		if r >= '0' && r <= '9' || r >= 'A' && r <= 'F' {
			hex.WriteRune(r)
		}
	}
	s := hex.String()
	if len(s) < 6 {
		return DefaultName
	}
	return DefaultName + " " + s[len(s)-6:]
}

// Idme reads a factory identity field from vendor IDME or the device tree (on the 2nd gen Show 8's
// mainline kernel, from the eMMC: idmeFallback), empty on any error. These are written at
// manufacture and never change, so a caller reads once and holds it.
func Idme(name string) string {
	raw, err := readIDME(name, os.ReadFile)
	if err != nil {
		v, _ := idmeFallback(name)
		return v
	}
	return strings.TrimSpace(strings.TrimRight(string(raw), "\x00"))
}

func readIDME(name string, readFile func(string) ([]byte, error)) ([]byte, error) {
	paths := []string{"/proc/idme/" + name, "/proc/device-tree/idme/" + name + "/value"}
	for _, path := range paths {
		raw, err := readFile(path)
		if err == nil {
			return raw, nil
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("IDME field %s: %w", name, os.ErrNotExist)
}

// Color is the device's shell, worked out from two idme fields: productid2 is 0 on a black unit and a
// nonzero code on a white one, and the serial runs G090LF… on black against G090L9… on white. They
// confirm each other; a disagreement, or nothing to read, is left unknown rather than guessed.
func Color(productID2, serial string) string {
	const (
		black   = "black"
		white   = "white"
		unknown = "unknown"
	)

	prod := unknown
	if n, err := strconv.Atoi(strings.TrimSpace(productID2)); err == nil {
		if n == 0 {
			prod = black
		} else {
			prod = white
		}
	}

	ser := unknown
	if s := strings.TrimSpace(serial); len(s) > 5 {
		switch s[5] {
		case 'F', 'f':
			ser = black
		case '9':
			ser = white
		}
	}

	switch {
	case prod == unknown:
		return ser
	case ser == unknown:
		return prod
	case prod == ser:
		return prod
	default:
		return unknown
	}
}

// LabelMax is the most a DNS label holds, in bytes. An mDNS service's instance name is one label, so
// the device's name advertised as one (AirPlay, Spotify Connect, Sendspin, announcements) has to fit
// it: a name of 31 characters outside ASCII can take 124 bytes.
const LabelMax = 63

// CutName is name cut to at most n bytes, at a character boundary, so what is left is still the
// characters it started with. A name that fits comes back as it is.
func CutName(name string, n int) string {
	if len(name) <= n {
		return name
	}
	cut := 0
	for i := range name {
		if i > n {
			break
		}
		cut = i
	}
	return strings.TrimRight(name[:cut], " ")
}

// NodeName is the ESPHome node's own name, which it is advertised under over mDNS: the device's name as
// a slug (Slug), one DNS label long at most. A name with nothing in it a slug keeps (one in Greek,
// Cyrillic or Chinese, say) falls back to the name the device would have had with none, which carries
// the end of its MAC address, so two such devices are still two nodes. Home Assistant names the
// device's actions after it: esphome.<node>_<action>, with its dashes made underscores.
func NodeName(name, mac string) string {
	return strings.TrimRight(CutName(SlugOr(name, mac), LabelMax), "-")
}

// SlugOr is name's Slug, or, for a name with nothing in it a slug keeps (one in Greek, Cyrillic or
// Chinese, say), the slug of the name the device has with none set (NameFromMAC), which carries the end
// of its MAC address: never empty, and two such devices still differ.
func SlugOr(name, mac string) string {
	if slug := Slug(name); slug != "" {
		return slug
	}
	return Slug(NameFromMAC(mac))
}

// Slug turns a display name into a node name: the mDNS hostname and the ESPHome node's own name.
// "Living Room" becomes "living-room". It is not what an entity id is built from — see EntitySlug.
func Slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}
