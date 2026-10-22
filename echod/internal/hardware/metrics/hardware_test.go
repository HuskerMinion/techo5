package metrics

import (
	"os"
	"path/filepath"
	"testing"
)

// board writes the files a biscuit has, with the values it reported, so the parsing is tested against
// what the device actually says rather than against what this machine happens to have.
func board(t *testing.T) Reader {
	t.Helper()
	root := t.TempDir()

	for path, body := range map[string]string{
		"sys/class/thermal/thermal_zone0/type": "mtktswmt\n",
		"sys/class/thermal/thermal_zone0/temp": "46000\n",
		"sys/class/thermal/thermal_zone1/type": "mtktscpu\n",
		"sys/class/thermal/thermal_zone1/temp": "45500\n",
		"sys/class/thermal/thermal_zone2/type": "mtkts1\n",
		"sys/class/thermal/thermal_zone2/temp": "43400\n",
		"sys/devices/system/cpu/present":       "0-3\n",
		"sys/devices/system/cpu/online":        "0-1\n",
		"proc/loadavg":                         "7.40 7.30 7.25 5/623 6841\n",
		"proc/meminfo":                         "MemTotal:         482956 kB\nMemFree:           25708 kB\nMemAvailable:     247308 kB\n",
		// Which index the sensor lands on is not fixed, so put it elsewhere to prove it is searched for.
		"sys/bus/iio/devices/iio:device2/illuminance0_input": "556\n",
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Reader{Root: root}
}

func TestTemperaturesComeBackByZoneNameInDegrees(t *testing.T) {
	temps := board(t).Temperatures()

	for zone, want := range map[string]float64{"mtktscpu": 45.5, "mtktswmt": 46, "mtkts1": 43.4} {
		got, ok := temps[zone]
		if !ok {
			t.Errorf("%s missing", zone)
			continue
		}
		if got != want {
			t.Errorf("%s = %v°C, want %v", zone, got, want)
		}
	}
	if len(temps) != 3 {
		t.Errorf("found %d zones, want 3", len(temps))
	}
}

// The 2nd gen Show 5 has more than ten zones and the CPU's is past the first ten, and some zones
// name themselves without a temperature. A mainline kernel calls the CPU something else.
func TestCPUTemperatureIsFoundByName(t *testing.T) {
	if got := CPUTemperature(board(t).Temperatures()); !got.Known || got.Value != 45.5 {
		t.Errorf("biscuit CPU = %+v, want 45.5", got)
	}

	root := t.TempDir()
	write := func(path, body string) {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("sys/class/thermal/thermal_zone2/type", "cpu_virtual\n")
	write("sys/class/thermal/thermal_zone12/type", "mtktscpu\n")
	write("sys/class/thermal/thermal_zone12/temp", "48300\n")
	if got := CPUTemperature(Reader{Root: root}.Temperatures()); !got.Known || got.Value != 48.3 {
		t.Errorf("Show 5 CPU = %+v, want 48.3", got)
	}

	// Two zones by the same name: the lower numbered one is taken, not whichever the listing ends on.
	write("sys/class/thermal/thermal_zone1/type", "mtktscpu\n")
	write("sys/class/thermal/thermal_zone1/temp", "44100\n")
	if got := CPUTemperature(Reader{Root: root}.Temperatures()); !got.Known || got.Value != 44.1 {
		t.Errorf("CPU with two zones of its name = %+v, want 44.1, thermal_zone1's", got)
	}

	mainline := map[string]float64{"tzts1": 39, "cpu-thermal": 41.2}
	if got := CPUTemperature(mainline); !got.Known || got.Value != 41.2 {
		t.Errorf("mainline CPU = %+v, want 41.2", got)
	}

	// No thermal driver: unknown, not zero, and not some other zone passed off as the CPU.
	if got := CPUTemperature(map[string]float64{"tzts1": 39}); got.Known {
		t.Errorf("CPU with no CPU zone = %v, want unknown", got.Value)
	}
}

func TestBootTime(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "proc"), 0o755); err != nil {
		t.Fatal(err)
	}
	stat := "cpu  1 2 3 4\nintr 5\nctxt 6\nbtime 1760000000\nprocesses 7\n"
	if err := os.WriteFile(filepath.Join(root, "proc/stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := Reader{Root: root}.BootTime()
	if !ok || got.Unix() != 1760000000 {
		t.Errorf("boot time = %v %v, want 1760000000", got.Unix(), ok)
	}

	if _, ok := (Reader{Root: t.TempDir()}).BootTime(); ok {
		t.Error("a missing /proc/stat gave a boot time")
	}
}

// Present and online differ on this hardware, and that difference is the reason to report both.
func TestCoresReportsPresentAndOnline(t *testing.T) {
	present, online := board(t).Cores()

	if !present.Known || present.Value != 4 {
		t.Errorf("present = %+v, want 4", present)
	}
	if !online.Known || online.Value != 2 {
		t.Errorf("online = %+v, want 2", online)
	}
}

func TestCoreListForms(t *testing.T) {
	for list, want := range map[string]float64{
		"0-3":     4,
		"0-1":     2,
		"0":       1,
		"0-1,3":   3,
		"0,2,4-5": 4,
	} {
		root := t.TempDir()
		path := filepath.Join(root, "sys/devices/system/cpu/online")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(list+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		if got := (Reader{Root: root}).count("sys/devices/system/cpu/online"); got.Value != want {
			t.Errorf("%q counted %v, want %v", list, got.Value, want)
		}
	}
}

func TestLoadAndMemory(t *testing.T) {
	r := board(t)

	one, five := r.Load()
	if !one.Known || one.Value != 7.40 || five.Value != 7.30 {
		t.Errorf("load = %v, %v, want 7.40, 7.30", one.Value, five.Value)
	}

	// Available, not free: free counts only untouched pages and reads alarmingly low on a machine that
	// is working properly — 25 MB here against 247 MB actually available.
	available, total := r.Memory()
	if !available.Known || available.Value != 247308 {
		t.Errorf("available = %+v, want 247308", available)
	}
	if !total.Known || total.Value != 482956 {
		t.Errorf("total = %+v, want 482956", total)
	}
}

// A board without a sensor reports nothing rather than zero, so Home Assistant shows unknown instead of
// a plausible lie.
func TestMissingFilesAreUnknownNotZero(t *testing.T) {
	r := Reader{Root: t.TempDir()}

	if temps := r.Temperatures(); len(temps) != 0 {
		t.Errorf("found %d zones on an empty root", len(temps))
	}
	present, online := r.Cores()
	one, _ := r.Load()
	available, _ := r.Memory()

	for name, got := range map[string]Reading{
		"present": present, "online": online, "load": one, "available": available,
		"lux": r.Lux(r.LuxPath()),
	} {
		if got.Known {
			t.Errorf("%s reported %v, want unknown", name, got.Value)
		}
	}
}

func TestLux(t *testing.T) {
	r := board(t)

	// Found where it actually is, not assumed to be the first iio device.
	path := r.LuxPath()
	if path == "" {
		t.Fatal("LuxPath() found no sensor")
	}

	if got := r.Lux(path); !got.Known || got.Value != 556 {
		t.Errorf("Lux() = %v, want 556", got)
	}
}

func TestLuxFallsBackToTheVendorDriver(t *testing.T) {
	root := t.TempDir()
	client := filepath.Join(root, "sys/bus/i2c/devices/0-0039")
	if err := os.MkdirAll(client, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(client, "als_lux"), []byte("8\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := Reader{Root: root}
	path := r.LuxPath()
	if want := filepath.Join(client, "als_lux"); path != want {
		t.Fatalf("LuxPath() = %q, want %q", path, want)
	}

	if got := r.Lux(path); !got.Known || got.Value != 8 {
		t.Errorf("Lux() = %v, want 8", got)
	}
}

func TestMainlineTemperatures(t *testing.T) {
	for _, radio := range []string{"Temperature = 47\n", "Temperature = invalid\n", "Temperature = 999\n", ""} {
		t.Run(radio, func(t *testing.T) {
			root := t.TempDir()
			for path, body := range map[string]string{
				"sys/class/thermal/thermal_zone2/type": "cpu-thermal\n",
				"sys/class/thermal/thermal_zone2/temp": "50219\n",
				"proc/net/wlan/get_temperature":        radio,
			} {
				full := filepath.Join(root, path)
				if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(body), 0644); err != nil {
					t.Fatal(err)
				}
			}
			got := (Reader{Root: root}).Temperatures()
			if got["mtktscpu"] != 50.219 {
				t.Fatalf("CPU: %v", got)
			}
			temp, known := got["mtktswmt"]
			if radio == "Temperature = 47\n" {
				if !known || temp != 47 {
					t.Fatalf("radio: %v", got)
				}
			} else if known {
				t.Fatalf("invalid radio reading published: %v", got)
			}
		})
	}
}

// The 2nd gen Show 8's MT7663 on the mainline kernel reports through hwmon, beside the CPU's own.
func TestMt76RadioTemperature(t *testing.T) {
	for _, tc := range []struct {
		milli string
		want  float64
		known bool
	}{{"41000\n", 41, true}, {"0\n", 0, false}, {"", 0, false}, {"999000\n", 0, false}} {
		root := t.TempDir()
		for path, body := range map[string]string{
			"sys/class/thermal/thermal_zone0/type": "cpu-thermal\n",
			"sys/class/thermal/thermal_zone0/temp": "39861\n",
			"sys/class/hwmon/hwmon0/name":          "cpu_thermal\n",
			"sys/class/hwmon/hwmon0/temp1_input":   "39861\n",
			"sys/class/hwmon/hwmon1/name":          "mt7615_phy0\n",
			"sys/class/hwmon/hwmon1/temp1_input":   tc.milli,
		} {
			full := filepath.Join(root, path)
			if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(body), 0644); err != nil {
				t.Fatal(err)
			}
		}
		temp, known := (Reader{Root: root}).Temperatures()["mtktswmt"]
		if known != tc.known || temp != tc.want {
			t.Errorf("radio from %q = %v (%v), want %v (%v)", tc.milli, temp, known, tc.want, tc.known)
		}
	}
}
