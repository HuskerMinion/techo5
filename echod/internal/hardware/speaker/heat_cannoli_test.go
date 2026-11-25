//go:build dot && cannoli

package speaker

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// The policy steps the speaker down as Fire OS's does: by the hottest zone, up at each trip and back
// down only once a trip's hysteresis is cleared, with soc_max standing in for cpu_virtual.
func TestHeatFollowsFireOSsPolicy(t *testing.T) {
	var h heatState
	for _, c := range []struct {
		zones  map[string]int
		cooler int
		want   int
	}{
		{map[string]int{"soc_max": 60000}, -1, 0},
		{map[string]int{"soc_max": 91000}, -1, 0}, // trips 1 and 2 ask for nothing of the speaker
		{map[string]int{"soc_max": 92000}, -1, 1},
		{map[string]int{"soc_max": 94500}, -1, 2},
		{map[string]int{"soc_max": 92500}, -1, 2}, // inside 94.0's 1.9 degrees
		{map[string]int{"soc_max": 92000}, -1, 1}, // out of them
		{map[string]int{"soc_max": 90200}, -1, 1}, // inside 92.0's
		{map[string]int{"soc_max": 90000}, -1, 0}, // out
		{map[string]int{"soc_max": 97000}, -1, 3}, // straight up through every trip
		{map[string]int{"soc_max": 50000, "skin_virtual": 58600}, -1, 2},
		{map[string]int{"soc_max": 50000}, 3, 3}, // the kernel's own cooler, where it has one
		{map[string]int{}, -1, 0},
	} {
		if got := h.update(c.zones, c.cooler); got != c.want {
			t.Errorf("%v, cooler %d: state %d, want %d", c.zones, c.cooler, got, c.want)
		}
	}
}

func TestHeatReadsTheKernelsZones(t *testing.T) {
	root := t.TempDir()
	write := func(path, v string) {
		t.Helper()
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(v+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i, z := range []struct{ name, temp string }{{"soc_max", "93000"}, {"cpu_little0", "91000"}, {"soc_max", "1"}} {
		write("sys/class/thermal/thermal_zone"+strconv.Itoa(i)+"/type", z.name)
		write("sys/class/thermal/thermal_zone"+strconv.Itoa(i)+"/temp", z.temp)
	}
	write("sys/class/thermal/cooling_device0/type", "thermal-cpufreq-0")
	write("sys/class/thermal/cooling_device0/cur_state", "5")
	old := heatRoot
	heatRoot = root
	t.Cleanup(func() { heatRoot = old })

	zones, cooler := readHeat()
	if zones["soc_max"] != 93000 || cooler != -1 {
		t.Errorf("read %v and cooler %d", zones, cooler)
	}
	write("sys/class/thermal/cooling_device1/type", "audio_cooler")
	write("sys/class/thermal/cooling_device1/cur_state", "2")
	if _, cooler = readHeat(); cooler != 2 {
		t.Errorf("the audio cooler reads %d", cooler)
	}
}
