//go:build dot && cannoli

package speaker

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The Echo Dot 5th gen turns its limiters down when it runs hot (asp's limits): Fire OS's thermal
// manager (ace_thermald, libacehal_thermal.so) steps the kernel's audio_cooler through 0 to 3 by the
// policy in /system/etc/.tp/thermal.policy.conf, the kernel sends that as a uevent (CDTYPE=audio_cooler,
// THERMAL_STATE=n), and the audio HAL hands it to libasp.so as a speaker power mode.
//
// None of that runs here. The zones are Amazon's thermal_module.ko (amazon_virtual_sensor_thermal.c,
// out of tree), which this boot does not load, and the manager is Android's. So echod follows the
// policy itself, from heatPolicy below, on whichever of its zones the kernel has. cpu_virtual is the
// one that is always there: its device tree node (cannoli-cheesecake.dtsi, cpu-virtual-sensor) is
// soc_max alone, at weight 1000, alpha 1000 and offset 0, so soc_max is cpu_virtual. The other three
// weigh the board's NTCs in ways only the module knows, and are used where the module made them.
// Where the kernel has an audio_cooler of its own, its state counts too, and the hotter of the two wins.

// heatRoot is where /sys is, for the tests.
var heatRoot = "/"

// heatPoll is the policy's own poll, 15 s on every zone.
const heatPoll = 15 * time.Second

// heatZone is one zone of the policy: its trips in millidegrees, the hysteresis each needs to clear on
// the way down, and the audio_cooler state each trip asks for.
type heatZone struct {
	name  string
	trips []int
	hyst  []int
	audio []int
}

// heatPolicy is thermal.policy.conf from 8349's system_a, the audio_cooler column of each zone.
var heatPolicy = []heatZone{
	{"cpu_virtual", []int{88000, 90000, 92000, 94000, 95500, 97000}, []int{0, 1900, 1900, 1900, 1400, 1400}, []int{0, 0, 1, 2, 3, 3}},
	{"skin_virtual", []int{56000, 57000, 57500, 58500, 59000, 60000}, []int{0, 900, 400, 900, 400, 900}, []int{0, 0, 1, 2, 3, 3}},
	{"case_virtual", []int{56000, 57000, 57500, 58500, 59000, 60000}, []int{0, 900, 400, 900, 400, 900}, []int{0, 0, 1, 2, 3, 3}},
	{"back_virtual", []int{56000, 57000, 57500, 58500, 59000, 60000}, []int{0, 900, 400, 900, 400, 900}, []int{0, 0, 1, 2, 3, 3}},
}

// next is how many of the zone's trips temp has reached, from at: up through every trip it is at or
// over, and down through every one it has fallen that trip's hysteresis below.
func (z heatZone) next(temp, at int) int {
	for at < len(z.trips) && temp >= z.trips[at] {
		at++
	}
	for at > 0 && temp < z.trips[at-1]-z.hyst[at-1] {
		at--
	}
	return at
}

// heatState follows the policy across polls: how far up each zone is.
type heatState struct {
	at map[string]int
}

// update takes one reading of the zones, in millidegrees by name, and the kernel's own audio_cooler
// state (-1 where it has none), and says what state the speaker should be throttled at.
func (h *heatState) update(zones map[string]int, cooler int) int {
	if h.at == nil {
		h.at = map[string]int{}
	}
	if _, ok := zones["cpu_virtual"]; !ok {
		if t, ok := zones["soc_max"]; ok {
			zones["cpu_virtual"] = t
		}
	}
	state := max(cooler, 0)
	for _, z := range heatPolicy {
		t, ok := zones[z.name]
		if !ok {
			delete(h.at, z.name)
			continue
		}
		at := z.next(t, h.at[z.name])
		h.at[z.name] = at
		if at > 0 {
			state = max(state, z.audio[at-1])
		}
	}
	return state
}

// readHeat is the kernel's thermal zones by name, in millidegrees, and the audio_cooler's state, or
// -1 where there is none.
func readHeat() (map[string]int, int) {
	read := func(path string) (int, bool) {
		b, err := os.ReadFile(path)
		if err != nil {
			return 0, false
		}
		v, err := strconv.Atoi(strings.TrimSpace(string(b)))
		return v, err == nil
	}
	zones := map[string]int{}
	paths, _ := filepath.Glob(filepath.Join(heatRoot, "sys/class/thermal/thermal_zone*"))
	for _, z := range paths {
		name, err := os.ReadFile(filepath.Join(z, "type"))
		if err != nil {
			continue
		}
		n := strings.TrimSpace(string(name))
		if _, seen := zones[n]; seen {
			continue
		}
		if t, ok := read(filepath.Join(z, "temp")); ok {
			zones[n] = t
		}
	}
	cooler := -1
	paths, _ = filepath.Glob(filepath.Join(heatRoot, "sys/class/thermal/cooling_device*"))
	for _, d := range paths {
		if name, err := os.ReadFile(filepath.Join(d, "type")); err == nil && strings.TrimSpace(string(name)) == "audio_cooler" {
			if s, ok := read(filepath.Join(d, "cur_state")); ok {
				cooler = s
			}
		}
	}
	return zones, cooler
}

// watchHeat follows the thermal policy for as long as playback runs, and keeps p.heat at its state.
func (p *Player) watchHeat(ctx context.Context) {
	var h heatState
	t := time.NewTicker(heatPoll)
	defer t.Stop()
	for {
		zones, cooler := readHeat()
		state := h.update(zones, cooler)
		if old := p.heat.Swap(int32(state)); int(old) != state {
			slog.Info("speaker thermal throttling", "state", state, "was", old, "cpu_virtual_mC", zones["cpu_virtual"])
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
