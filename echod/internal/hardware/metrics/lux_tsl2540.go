package metrics

import (
	"math"
	"path/filepath"
	"strconv"
	"strings"
)

// The Echo Dot 5th gen's light sensor, an ams TSL2540 at i2c 4-0039. Amazon's 5.4 kernel builds the IIO
// staging driver for it (CONFIG_TSL2540, drivers/staging/iio/light/tsl2540.c in the 6.5.5.5 GPL tree),
// which hands over the two raw channels and nothing more: in_intensity0_raw (visible),
// in_intensity1_raw (infrared), the gain as in_intensity_calibscale and in_intensity_integration_time in
// seconds. The tree's other TSL2540 driver, drivers/misc/ams_tsl2540*.c, has an als_lux of its own, but
// it is not built (CONFIG_SENSORS_TSL2540 is off).
//
// So lux is worked out here, as Fire OS works it out in libacehal_ambientLightSensor.so: two linear
// forms of the channels, one row of coefficients picked by the cover's color (idme alsparams' capcolor),
// blended by smootherstep on the infrared share and divided by the gain and the integration time; then a
// straight line through the unit's two factory points (idme alscal: the counts at 20 and 400 lx, taken at
// alsparams' gain and time). Read off that library's disassembly by echo-dot-assist (Gamer92000, MIT:
// src/hassmic/lux.h) and checked there against stock's own alsctrl with the same idme data. A unit with
// no calibration in idme has no reading, as stock answers 0 for good then.

// tsl2540Rows are the coefficients by capcolor: c0*(c1*vis + c2*ir) below the infrared share c6,
// c3*(c4*vis + c5*ir) above c7, blended between.
var tsl2540Rows = [8][8]float64{
	{212.3177, 53.07943, 53.07943, 243.4285, 60.85712, -9.29433, 0.24941, 1},
	{116.1907, 1.120936, -0.18, 24.30953, 6.077383, -1.01792, 0.329448, 1},
	{226.9058, 56.72645, -11.7879, 239.7658, 59.94146, -9.90576, 0.282603, 1},
	{560.9854676, 17.40776771, -2.591608011, 293.0102725, 73.25256822, -11.39621282, 0.251987727, 1},
	{37.65063, 9.412658, -2.15521, 38.92021, 9.730053, -1.48395, 0.282607, 1},
	{565.8752297, 11.63762545, -3.504493061, 246.9294843, 61.7323711, -9.858968887, 0.251987727, 1},
	{185.5159, 46.37898, 9.090086, 207.656, 51.91401, -9.66167, 0.268493, 1},
	{320.6533, 12.99457, -0.06072, 141.0411, 35.26027, -6.3573, 0.278486, 1},
}

// tsl2540Gains are the gain the formula takes for each of the driver's calibscale values, by alsgain's
// index (0 to 5).
var (
	tsl2540Gains       = [6]float64{0.5, 1, 4, 16, 64, 128}
	tsl2540Calibscales = [6]int{1, 2, 8, 32, 128, 256}
)

// tsl2540Cal is the unit's calibration from idme.
type tsl2540Cal struct {
	row, gain     int
	timeMS        float64
	vis20, ir20   float64
	vis400, ir400 float64
	have          bool
}

// tsl2540Path is the visible channel of a TSL2540 among the IIO devices, empty without one.
func (r Reader) tsl2540Path() string {
	iio, _ := filepath.Glob(r.path("sys/bus/iio/devices/iio:device*"))
	for _, dir := range iio {
		if name, _ := text(filepath.Join(dir, "name")); name != "tsl2540" {
			continue
		}
		at := filepath.Join(dir, "in_intensity0_raw")
		if _, err := number(at); err == nil {
			return at
		}
	}
	return ""
}

// tsl2540Lux is the room's lux from the TSL2540 whose visible channel is at path. The gain and the
// integration time are read back each time: whatever set them last (stock's ledcontroller, or the
// driver's defaults) is what the counts were taken at.
func (r Reader) tsl2540Lux(path string) Reading {
	dir := filepath.Dir(path)
	vis, err1 := number(path)
	ir, err2 := number(filepath.Join(dir, "in_intensity1_raw"))
	scale, err3 := number(filepath.Join(dir, "in_intensity_calibscale"))
	itime, err4 := number(filepath.Join(dir, "in_intensity_integration_time"))
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return Reading{}
	}
	cal := parseTSL2540Cal(r.idme("alsparams"), r.idme("alscal"))
	lux, ok := tsl2540Calibrated(vis, ir, int(scale), itime, cal)
	if !ok {
		return Reading{}
	}
	return known(lux)
}

// idme is one of Amazon's factory fields: /proc/idme on Amazon's kernels, or the device tree's idme node
// the bootloader writes, where a kernel has no /proc/idme.
func (r Reader) idme(field string) string {
	if s, err := text(r.path("proc/idme/" + field)); err == nil {
		return strings.Trim(s, "\x00")
	}
	s, _ := text(r.path("proc/device-tree/idme/" + field + "/value"))
	return strings.Trim(s, "\x00")
}

// parseTSL2540Cal reads idme's two strings as stock does: alsparams by key (alsgain an index 0 to 5,
// default 5; alstime in ms, default 721; capcolor a row, anything past 7 taken as 0), and alscal all nine
// hex fields or nothing ("ams_0_0=a,b,c ams_20_0=vis,x,ir ams_400_0=vis,x,ir"; the middle field and the
// 0 lx point are not used).
func parseTSL2540Cal(params, cal string) tsl2540Cal {
	k := tsl2540Cal{row: 0, gain: 5, timeMS: 721}
	for f := range strings.FieldsSeq(params) {
		key, v, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		switch key {
		case "alsgain":
			if n, err := strconv.Atoi(v); err == nil {
				k.gain = n
			}
		case "alstime":
			if t, err := strconv.ParseFloat(v, 64); err == nil {
				k.timeMS = t
			}
		case "capcolor":
			if n, err := strconv.Atoi(v); err == nil {
				k.row = n
			}
		}
	}
	if k.row < 0 || k.row > 7 {
		k.row = 0
	}
	if k.gain < 0 || k.gain > 5 {
		k.gain = 5
	}

	points := map[string][3]float64{}
	for f := range strings.FieldsSeq(cal) {
		key, v, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		parts := strings.Split(v, ",")
		if len(parts) != 3 {
			return k
		}
		var p [3]float64
		for i, s := range parts {
			n, err := strconv.ParseUint(strings.TrimSpace(s), 16, 32)
			if err != nil {
				return k
			}
			p[i] = float64(n)
		}
		points[key] = p
	}
	_, ok0 := points["ams_0_0"]
	p20, ok20 := points["ams_20_0"]
	p400, ok400 := points["ams_400_0"]
	if ok0 && ok20 && ok400 {
		k.vis20, k.ir20, k.vis400, k.ir400 = p20[0], p20[2], p400[0], p400[2]
		k.have = true
	}
	return k
}

// tsl2540Gain is the formula's gain for a calibscale the driver reports, 0 for one stock never sets.
func tsl2540Gain(calibscale int) float64 {
	for i, c := range tsl2540Calibscales {
		if c == calibscale {
			return tsl2540Gains[i]
		}
	}
	return 0
}

// tsl2540Raw is stock's uncalibrated lux for the counts at gain and tMS milliseconds.
func tsl2540Raw(vis, ir, gain, tMS float64, row int) float64 {
	c := tsl2540Rows[row&7]
	if vis <= 0 || gain <= 0 || tMS <= 0 {
		return 0
	}
	x := (ir/vis - c[6]) / (c[7] - c[6])
	s := 0.0
	switch {
	case x >= 1:
		s = 1
	case x > 0:
		s = x * x * x * (10 + x*(-15+6*x))
	}
	return ((1-s)*c[0]*(c[1]*vis+c[2]*ir) + s*c[3]*(c[4]*vis+c[5]*ir)) / (gain * tMS)
}

// tsl2540Calibrated is the lux on the line through the unit's 20 and 400 lx points. ok is false without a
// calibration, with a degenerate one, at a calibscale stock never sets or with no integration time.
func tsl2540Calibrated(vis, ir float64, calibscale int, itimeS float64, k tsl2540Cal) (float64, bool) {
	gain := tsl2540Gain(calibscale)
	if !k.have || gain <= 0 || itimeS <= 0 {
		return 0, false
	}
	g := tsl2540Gains[k.gain]
	u := tsl2540Raw(vis, ir, gain, itimeS*1000, k.row)
	u20 := tsl2540Raw(k.vis20, k.ir20, g, k.timeMS, k.row)
	u400 := tsl2540Raw(k.vis400, k.ir400, g, k.timeMS, k.row)
	if u400-u20 <= 0 {
		return 0, false
	}
	l := 20 + (u-u20)*(400-20)/(u400-u20)
	if math.IsNaN(l) || math.IsInf(l, 0) {
		return 0, false
	}
	return max(l, 0), true
}
