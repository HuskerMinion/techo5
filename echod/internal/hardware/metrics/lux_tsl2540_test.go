package metrics

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// An idme in the shape a unit's has, with made-up numbers. The formula itself was checked against
// stock's alsctrl in echo-dot-assist (tests/unit/lux_test.c); here the line through the two factory points
// is checked against values worked out from them.
const (
	cannoliParams = "alstime=600 alsgain=5 capcolor=2 "
	cannoliCal    = "ams_0_0=0,0,0 ams_20_0=280,0,40 ams_400_0=3800,0,800 "
)

func TestTSL2540Lux(t *testing.T) {
	k := parseTSL2540Cal(cannoliParams, cannoliCal)
	if !k.have || k.row != 2 || k.gain != 5 || k.timeMS != 600 || k.vis20 != 0x280 || k.ir400 != 0x800 {
		t.Fatalf("calibration %+v", k)
	}
	if got := int(tsl2540Raw(1866, 242, tsl2540Gain(256), 700, 4)); got != 7 {
		t.Errorf("1866/242 uncalibrated on row 4: %d, stock 7", got)
	}
	for _, c := range []struct {
		vis, ir float64
		want    int
	}{{1866, 242, 46}, {1939, 249, 48}} {
		lux, ok := tsl2540Calibrated(c.vis, c.ir, 256, 0.700, k)
		if !ok || int(lux) != c.want {
			t.Errorf("%v/%v: %v (%v), want %d", c.vis, c.ir, lux, ok, c.want)
		}
	}
	if got := int(tsl2540Raw(1962, 252, 128, 700, 0)); got != 278 {
		t.Errorf("row 0 uncalibrated: %d, want 278", got)
	}
	// The line through the 20 and 400 lx points meets no counts a little above 0 lx, as stock's does.
	if lux, ok := tsl2540Calibrated(0, 0, 256, 0.700, k); !ok || lux < 2 || lux > 2.2 {
		t.Errorf("dark: %v (%v), want about 2.07", lux, ok)
	}
}

// No calibration, a gain stock never sets or no integration time: no reading, as stock gives none.
func TestTSL2540WithoutCalibrationHasNoReading(t *testing.T) {
	none := parseTSL2540Cal(cannoliParams, "ams_0_0=0,0,0")
	if _, ok := tsl2540Calibrated(1866, 242, 256, 0.700, none); ok {
		t.Error("a reading without alscal")
	}
	k := parseTSL2540Cal(cannoliParams, cannoliCal)
	if _, ok := tsl2540Calibrated(1866, 242, 100, 0.700, k); ok {
		t.Error("a reading at calibscale 100")
	}
	if _, ok := tsl2540Calibrated(1866, 242, 256, 0, k); ok {
		t.Error("a reading with no integration time")
	}
	if d := parseTSL2540Cal("capcolor=9 alsgain=7", ""); d.row != 0 || d.gain != 5 || d.timeMS != 721 {
		t.Errorf("out of range idme not taken as stock's defaults: %+v", d)
	}
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for path, body := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// LuxPath finds the TSL2540 among the IIO devices, and Lux reads it with idme's calibration, from
// /proc/idme or, without it, the device tree's idme node.
func TestTheDot5LightSensorIsFound(t *testing.T) {
	for _, idme := range []string{"proc/idme/%s", "proc/device-tree/idme/%s/value"} {
		root := t.TempDir()
		dev := "sys/bus/iio/devices/iio:device1/"
		files := map[string]string{
			"sys/bus/iio/devices/iio:device0/name": "mt6395-auxadc\n",
			dev + "name":                           "tsl2540\n",
			dev + "in_intensity0_raw":              "1866\n",
			dev + "in_intensity1_raw":              "242\n",
			dev + "in_intensity_calibscale":        "256\n",
			dev + "in_intensity_integration_time":  "0.700\n",
		}
		files[fmt.Sprintf(idme, "alsparams")] = cannoliParams + "\x00"
		files[fmt.Sprintf(idme, "alscal")] = cannoliCal + "\x00"
		writeFiles(t, root, files)

		r := Reader{Root: root}
		path := r.LuxPath()
		if filepath.Base(path) != "in_intensity0_raw" {
			t.Fatalf("%s: LuxPath %q", idme, path)
		}
		if got := r.Lux(path); !got.Known || int(got.Value) != 46 {
			t.Errorf("%s: Lux %+v, want 46", idme, got)
		}
	}
}

// The MT7663's wifi_temp zone is the radio, and 0 there is no answer; soc_max is the CPU where there is
// no mtktscpu.
func TestTheDot5Temperatures(t *testing.T) {
	for _, c := range []struct {
		wifi  string
		known bool
	}{{"43000\n", true}, {"0\n", false}} {
		root := t.TempDir()
		writeFiles(t, root, map[string]string{
			"sys/class/thermal/thermal_zone0/type": "soc_max\n",
			"sys/class/thermal/thermal_zone0/temp": "51200\n",
			"sys/class/thermal/thermal_zone9/type": "wifi_temp\n",
			"sys/class/thermal/thermal_zone9/temp": c.wifi,
		})
		temps := (Reader{Root: root}).Temperatures()
		if radio, ok := temps["mtktswmt"]; ok != c.known || (ok && radio != 43) {
			t.Errorf("wifi_temp %q: radio %v (%v)", c.wifi, radio, ok)
		}
		if cpu := CPUTemperature(temps); !cpu.Known || cpu.Value != 51.2 {
			t.Errorf("CPU %+v, want soc_max's 51.2", cpu)
		}
	}
}
