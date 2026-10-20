// Package metrics reads what the machine says about itself: how hot it is, how many cores are
// running, how much memory is left, and where on the network it can be found.
//
// The readings are files in sysfs or proc, so one costs nothing and a missing one is not an error — a
// board that does not have a sensor simply has no value for it.
package metrics

import (
	"cmp"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Paths are relative to a root so a test can point them at a directory of fixtures rather than at the
// machine running the test.
type Reader struct {
	Root string
}

// Reading is one number the board reported, and whether it reported one at all.
type Reading struct {
	Value float64
	Known bool
}

func known(v float64) Reading { return Reading{Value: v, Known: true} }

// Temperatures are the thermal zones by the name the kernel gives them, in degrees. On a biscuit that
// is mtktscpu for the CPU and mtktswmt for the wifi and Bluetooth combo chip, plus board sensors.
//
// Every zone is looked at, not a fixed count: the 2nd gen Show 5 has sixteen and puts the CPU at
// twelve, and nothing says another board stops there. A zone that names itself and reports no
// temperature, as the virtual ones on the Show 5 do, is left out. Zones are taken in the kernel's
// order, thermal_zone2 before thermal_zone12, and where two share a name the first is the one kept.
//
// The kernel reports millidegrees.
func (r Reader) Temperatures() map[string]float64 {
	out := map[string]float64{}
	zones, _ := filepath.Glob(r.path("sys/class/thermal/thermal_zone*"))
	slices.SortFunc(zones, func(a, b string) int { return cmp.Compare(zoneNumber(a), zoneNumber(b)) })
	for _, zone := range zones {
		name, err := text(zone + "/type")
		if err != nil {
			continue
		}
		if _, seen := out[name]; seen {
			continue
		}
		milli, err := number(zone + "/temp")
		if err != nil {
			continue
		}
		out[name] = milli / 1000
	}
	if _, exists := out["mtktscpu"]; !exists {
		if temp, ok := out["cpu-thermal"]; ok {
			out["mtktscpu"] = temp
		}
	}
	if _, exists := out["mtktswmt"]; !exists {
		if raw, err := text(r.path("proc/net/wlan/get_temperature")); err == nil {
			if key, value, ok := strings.Cut(raw, "="); ok && strings.TrimSpace(key) == "Temperature" {
				if temp, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && temp >= -40 && temp <= 150 {
					out["mtktswmt"] = temp
				}
			}
		}
	}
	if _, exists := out["mtktswmt"]; !exists {
		if temp, ok := r.mt76Temperature(); ok {
			out["mtktswmt"] = temp
		}
	}
	return out
}

// mt76Temperature is the radio's temperature on a mainline kernel's mt76 driver: the 2nd gen Show 8's
// MT7663 reports it through a hwmon device named mt7615_phy0, where the kernel's driver offers it, in
// millidegrees. Zero is the firmware not answering, not a reading: a
// radio that is on is never that cold.
func (r Reader) mt76Temperature() (float64, bool) {
	devices, _ := filepath.Glob(r.path("sys/class/hwmon/hwmon*"))
	for _, dev := range devices {
		name, err := text(dev + "/name")
		if err != nil || !strings.HasPrefix(name, "mt7615_phy") {
			continue
		}
		milli, err := number(dev + "/temp1_input")
		if temp := milli / 1000; err == nil && temp > 0 && temp <= 150 {
			return temp, true
		}
	}
	return 0, false
}

// zoneNumber is the n of a thermal_zoneN path, or one past any zone for a name that has none.
func zoneNumber(path string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(path), "thermal_zone"))
	if err != nil {
		return math.MaxInt
	}
	return n
}

// cpuZones are the names the CPU's zone goes by, best first. Amazon's MediaTek kernels call it
// mtktscpu, as the Dot's 3.18 and the Show 5's 4.9 do; a mainline kernel names it after its device
// tree node, cpu-thermal on the MT8183, and some older trees spell that with an underscore.
var cpuZones = []string{"mtktscpu", "cpu-thermal", "cpu_thermal", "soc-thermal", "soc_thermal"}

// CPUTemperature picks the CPU out of what Temperatures found. Unknown on a kernel with no thermal
// driver at all. The mainline Show 8's kernel has its driver built in, and the zone is cpu-thermal.
func CPUTemperature(temps map[string]float64) Reading {
	for _, zone := range cpuZones {
		if v, ok := temps[zone]; ok {
			return known(v)
		}
	}
	return Reading{}
}

// BootTime is when the machine started, from the kernel's btime in /proc/stat. The kernel works it
// out from the wall clock on every read, so it moves when the clock is set and is right once NTP
// has run.
func (r Reader) BootTime() (time.Time, bool) {
	body, err := os.ReadFile(r.path("proc/stat"))
	if err != nil {
		return time.Time{}, false
	}
	for line := range strings.SplitSeq(string(body), "\n") {
		value, ok := strings.CutPrefix(line, "btime ")
		if !ok {
			continue
		}
		secs, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || secs <= 0 {
			return time.Time{}, false
		}
		return time.Unix(secs, 0), true
	}
	return time.Time{}, false
}

// Cores is how many the kernel could use and how many are online. They differ: this kernel hotplugs
// cores, so a device with four can be running two, and any figure about CPU usage means something
// different depending on which it is.
func (r Reader) Cores() (present, online Reading) {
	return r.count("sys/devices/system/cpu/present"), r.count("sys/devices/system/cpu/online")
}

// Load is the one and five minute load averages.
func (r Reader) Load() (one, five Reading) {
	line, err := text(r.path("proc/loadavg"))
	if err != nil {
		return Reading{}, Reading{}
	}

	fields := strings.Fields(line)
	if len(fields) < 2 {
		return Reading{}, Reading{}
	}
	return parse(fields[0]), parse(fields[1])
}

// Memory is what the kernel says is available and what the board has, in kilobytes. Available rather
// than free: free counts only untouched pages and reads alarmingly low on a machine that is working
// properly.
func (r Reader) Memory() (available, total Reading) {
	body, err := os.ReadFile(r.path("proc/meminfo"))
	if err != nil {
		return Reading{}, Reading{}
	}

	for line := range strings.SplitSeq(string(body), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		kb := parse(strings.TrimSuffix(strings.TrimSpace(value), " kB"))
		switch key {
		case "MemAvailable":
			available = kb
		case "MemTotal":
			total = kb
		}
	}
	return available, total
}

// Wifi is what the radio reports: the signal in dBm, and the bytes carried since boot.
//
// The kernel keeps the signal as an unsigned byte, so anything above 127 is a negative dBm.
func (r Reader) Wifi() (signal, rx, tx Reading) {
	if body, err := os.ReadFile(r.path("proc/net/wireless")); err == nil {
		for line := range strings.SplitSeq(string(body), "\n") {
			name, rest, ok := strings.Cut(line, ":")
			if !ok || strings.TrimSpace(name) != wifiInterface {
				continue
			}
			if fields := strings.Fields(rest); len(fields) >= 3 {
				if level := parse(strings.TrimSuffix(fields[2], ".")); level.Known {
					if level.Value > 127 {
						level.Value -= 256
					}
					signal = level
				}
			}
		}
	}

	body, err := os.ReadFile(r.path("proc/net/dev"))
	if err != nil {
		return signal, Reading{}, Reading{}
	}

	for line := range strings.SplitSeq(string(body), "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) != wifiInterface {
			continue
		}
		if fields := strings.Fields(rest); len(fields) >= 9 {
			rx, tx = parse(fields[0]), parse(fields[8])
		}
	}
	return signal, rx, tx
}

// wifiInterface is the only one on this board.
const wifiInterface = "wlan0"

// count reads a cpu list like "0-3" or "0-1,3" and reports how many it names.
func (r Reader) count(path string) Reading {
	list, err := text(r.path(path))
	if err != nil {
		return Reading{}
	}

	var n float64
	for part := range strings.SplitSeq(list, ",") {
		lo, hi, ranged := strings.Cut(part, "-")
		first, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil {
			continue
		}
		if !ranged {
			n++
			continue
		}
		last, err := strconv.Atoi(strings.TrimSpace(hi))
		if err != nil {
			continue
		}
		n += float64(last - first + 1)
	}
	if n == 0 {
		return Reading{}
	}
	return known(n)
}

func (r Reader) path(p string) string {
	if r.Root == "" {
		return "/" + p
	}
	return r.Root + "/" + p
}

func text(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

func number(path string) (float64, error) {
	body, err := text(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(body, 64)
}

func parse(s string) Reading {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return Reading{}
	}
	return known(v)
}
