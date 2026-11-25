package asp

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gainSection is a biquad that is only a gain, and flat the identity.
func gainSection(db float64) iirSection { return iirSection{b0: dbToLinear(db)} }

var flat = iirSection{b0: 1}

// writeIIR writes sections as the vendor lays them out: sets of four, rows a1, a2, b0, b1, b2.
func writeIIR(t *testing.T, path string, s []iirSection) {
	t.Helper()
	for len(s)%4 != 0 {
		s = append(s, flat)
	}
	var b strings.Builder
	b.WriteString("{\n    \"Coefficients\":\n    [\n")
	for set := 0; set < len(s); set += 4 {
		fmt.Fprintf(&b, "        // set %d, 4 biquads\n", set/4+1)
		rows := []func(iirSection) float64{
			func(v iirSection) float64 { return v.a1 }, func(v iirSection) float64 { return v.a2 },
			func(v iirSection) float64 { return v.b0 }, func(v iirSection) float64 { return v.b1 },
			func(v iirSection) float64 { return v.b2 },
		}
		for r, get := range rows {
			b.WriteString("        ")
			for k := range 4 {
				fmt.Fprintf(&b, "%.17g", get(s[set+k]))
				if k < 3 || r < 4 || set+4 < len(s) {
					b.WriteString(",")
				}
			}
			b.WriteString(" // row\n")
		}
	}
	b.WriteString("    ]\n}\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// cannoliAFE is the two limiter sections as the Dot 5's AFE.cfg has them, among other things that name
// them, with its comments and a trailing comma the vendor's parser takes.
const cannoliAFE = `// Comments are accepted
{
    "Hardware Definition": { "Name" : "Cannoli" },
    "Playback": { "Algorithms": {
        "MBLimiter_LR"            : "Multiband Limiter",
        "DriverFBL_LR"            : "Fullband Limiter",
    } },
    "Multiband Limiter":
        {
            "Bypass"            : %s,
            "Num Bands"         : 4,
            "FilterBank FC"     : [200, 900, 5000], // Always 4 bands
            "Input Volume"      : [0.0, 0.0, 0.0, 0.0],
            "Hold Time"         : [0.0, 0.0, 0.0, 0.0],
            "Release Time"      : [500.0, 300.0, 200.0, 200.0],
            "Threshold"         : [-2.0, -1.0, -1.0, -1.0],
            "LookAhead Time"    : 2.0
        },
    "Fullband Limiter":
        {
              "Bypass"          : false,
              "InputVolume_dB"  : 0.0,
              "Threshold_dB"    : -0.1,
              "LookAhead_ms"    : 1,
              "HoldTime_ms"     : 24,
              "ReleaseTime_ms"  : 8,
              "Enable Excursion Limiter"    : false,
        }
}
`

// cannoliDir is a directory laid out as the Dot 5's, with driver as its driver EQ and quiet as its
// quietest volume EQ; every other volume EQ is flat.
func cannoliDir(t *testing.T, driver []iirSection, quiet []iirSection) string {
	t.Helper()
	dir := t.TempDir()
	writeIIR(t, filepath.Join(dir, Cannoli.IIR), driver)
	for i, b := range Cannoli.VolEQ {
		v := []iirSection{flat, flat, flat, flat}
		if i == 0 && quiet != nil {
			v = quiet
		}
		writeIIR(t, filepath.Join(dir, b.Name), v)
	}
	if err := os.WriteFile(filepath.Join(dir, Cannoli.Limits), []byte(fmt.Sprintf(cannoliAFE, "false")), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func sine(hz, db float64, seconds float64) []float32 {
	x := make([]float32, int(seconds*Rate)/1024*1024)
	a := dbToLinear(db)
	for i := range x {
		x[i] = float32(a * math.Sin(2*math.Pi*hz*float64(i)/Rate))
	}
	return x
}

func run(t *testing.T, tun *Tuning, x []float32, volume float64) []float32 {
	t.Helper()
	c, err := tun.Chain(1024)
	if err != nil {
		t.Fatal(err)
	}
	c.Volume(volume)
	for i := 0; i+1024 <= len(x); i += 1024 {
		c.Process(x[i : i+1024])
	}
	return x
}

func tailRMS(x []float32) float64 {
	var s float64
	tail := x[len(x)/2:]
	for _, v := range tail {
		s += float64(v) * float64(v)
	}
	return 10 * math.Log10(s/float64(len(tail)))
}

func peakDB(x []float32) float64 {
	var p float64
	for _, v := range x {
		p = math.Max(p, math.Abs(float64(v)))
	}
	return 20 * math.Log10(p)
}

// A flat tuning passes a quiet tone through as it came, only later: the crossover's bands sum back flat
// and neither limiter has anything to do under its ceiling.
func TestAFlatCannoliChainLeavesQuietMusicAlone(t *testing.T) {
	tun, err := LoadSet(cannoliDir(t, []iirSection{flat}, nil), Cannoli)
	if err != nil {
		t.Fatal(err)
	}
	for _, hz := range []float64{60, 500, 3000, 10000} {
		in := sine(hz, -20, 1)
		want := tailRMS(in)
		if got := tailRMS(run(t, tun, in, 1)); math.Abs(got-want) > 0.1 {
			t.Errorf("%g Hz at -20 dB came out %.2f dB, want %.2f", hz, got, want)
		}
	}
}

// The driver EQ's lift is what the limiters are there for: a tone lifted past full scale comes out
// under the full-band ceiling, every sample of it.
func TestTheCannoliLimitersHoldTheLift(t *testing.T) {
	tun, err := LoadSet(cannoliDir(t, []iirSection{gainSection(18)}, nil), Cannoli)
	if err != nil {
		t.Fatal(err)
	}
	for _, hz := range []float64{80, 1000, 6000} {
		out := run(t, tun, sine(hz, -3, 1), 1)
		if p := peakDB(out); p > -0.1+0.01 {
			t.Errorf("%g Hz lifted 18 dB peaks at %+.2f dBFS, past the -0.1 dB ceiling", hz, p)
		}
		// Held to its band's ceiling, not crushed to nothing.
		if r := tailRMS(out); r < -6 {
			t.Errorf("%g Hz came out at %.1f dB RMS, limited far past its ceiling", hz, r)
		}
	}
}

// The volume EQ follows the dial: the quietest one only below a tenth of full volume.
func TestTheCannoliVolumeEQFollowsTheVolume(t *testing.T) {
	tun, err := LoadSet(cannoliDir(t, []iirSection{flat}, []iirSection{gainSection(6), flat, flat, flat}), Cannoli)
	if err != nil {
		t.Fatal(err)
	}
	quiet := tailRMS(run(t, tun, sine(1000, -30, 1), 0.05))
	loud := tailRMS(run(t, tun, sine(1000, -30, 1), 0.5))
	if d := quiet - loud; math.Abs(d-6) > 0.1 {
		t.Errorf("the quietest volume EQ added %.2f dB, want 6", d)
	}

	// And a chain that was told one volume and then another takes the new one at once.
	c, err := tun.Chain(1024)
	if err != nil {
		t.Fatal(err)
	}
	x := sine(1000, -30, 1)
	c.Volume(0.05)
	c.Volume(0.9)
	for i := 0; i < len(x); i += 1024 {
		c.Process(x[i : i+1024])
	}
	if got := tailRMS(x); math.Abs(got-loud) > 0.1 {
		t.Errorf("back at 0.9 the tone is %.2f dB, want %.2f", got, loud)
	}
}

// A unit that names its speaker vendor gets that vendor's file, where there is one; a name that is a
// path is not one.
func TestTheCannoliDriverEQIsTheSpeakerVendors(t *testing.T) {
	dir := cannoliDir(t, []iirSection{flat}, nil)
	writeIIR(t, filepath.Join(dir, "GSE_"+Cannoli.IIR), []iirSection{gainSection(-6)})
	idme := filepath.Join(t.TempDir(), "speaker_vendor")
	was := SpeakerVendor
	SpeakerVendor = idme
	t.Cleanup(func() { SpeakerVendor = was })

	for _, c := range []struct{ vendor, want string }{
		{"gse\n", "GSE_" + Cannoli.IIR},
		{"GTK", Cannoli.IIR}, // no file of its own: the default
		{"../GSE", Cannoli.IIR},
		{"", Cannoli.IIR},
	} {
		if err := os.WriteFile(idme, []byte(c.vendor), 0o600); err != nil {
			t.Fatal(err)
		}
		tun, err := LoadSet(dir, Cannoli)
		if err != nil {
			t.Fatal(err)
		}
		if tun.DriverFile() != c.want {
			t.Errorf("speaker vendor %q loaded %s, want %s", c.vendor, tun.DriverFile(), c.want)
		}
	}
}

// What would put the lift on the driver unguarded, or is not a filter at all, is refused, and the
// speaker plays untuned instead.
func TestTheCannoliSetRefusesWhatItCannotRun(t *testing.T) {
	cases := map[string]func(dir string){
		"an unstable biquad": func(dir string) {
			writeIIR(t, filepath.Join(dir, Cannoli.IIR), []iirSection{{a1: -2.1, a2: 1.2, b0: 1}})
		},
		"a bypassed limiter": func(dir string) {
			os.WriteFile(filepath.Join(dir, Cannoli.Limits), []byte(fmt.Sprintf(cannoliAFE, "true")), 0o600)
		},
		"an AFE.cfg with no limiters": func(dir string) {
			os.WriteFile(filepath.Join(dir, Cannoli.Limits), []byte(`{"Hardware Definition": {}}`), 0o600)
		},
		"a coefficient file of the wrong length": func(dir string) {
			os.WriteFile(filepath.Join(dir, Cannoli.VolEQ[3].Name), []byte(`{"Coefficients": [1, 0, 0]}`), 0o600)
		},
		"a missing volume EQ": func(dir string) { os.Remove(filepath.Join(dir, Cannoli.VolEQ[9].Name)) },
	}
	for name, spoil := range cases {
		dir := cannoliDir(t, []iirSection{flat}, nil)
		spoil(dir)
		if _, err := LoadSet(dir, Cannoli); err == nil {
			t.Errorf("%s loaded", name)
		}
	}
}

// The full-band limiter's hold keeps the gain down for its 24 ms after a peak before it releases, so
// it does not pump at the bass's own period.
func TestALimiterHolds(t *testing.T) {
	l := newLimiterWith(-6, 8*time.Millisecond, 24*time.Millisecond, time.Millisecond, Rate)
	x := make([]float32, 480)
	for i := range x {
		x[i] = 1
	}
	l.process(x)
	l.process(make([]float32, 2*Rate/1000)) // the peak leaves the lookahead
	low := l.gain
	if low > 0.6 {
		t.Fatalf("a full-scale tone under a -6 dB ceiling left the gain at %.3f", low)
	}
	l.process(make([]float32, 15*Rate/1000)) // and 15 ms more is still inside the hold
	if l.gain != low {
		t.Errorf("the gain went from %.3f to %.3f inside the hold", low, l.gain)
	}
	l.process(make([]float32, 60*Rate/1000))
	if l.gain < 0.99 {
		t.Errorf("60 ms after the hold the gain is still %.3f", l.gain)
	}
}

// The real Dot 5 tuning (ECHOLOCAL_VENDOR_DIR, ECHOLOCAL_VENDOR_SET=cannoli): the driver EQ lifts the
// bass and cuts the presence as measured off 8349, and the chain keeps every sample under its ceiling.
func TestVendorCannoliHasTheShapeWeMeasured(t *testing.T) {
	v := vendorTuning(t)
	if v.set.Name != "cannoli" {
		t.Skipf("the %s tuning", v.set.Name)
	}
	ref := iirResponse(v.driver, 1000)
	for _, c := range []struct{ hz, lo, hi float64 }{
		{80, 13, 20}, {100, 13, 20}, {3000, -10, -4}, {500, -1, 2}, {6000, 2, 8},
	} {
		if got := iirResponse(v.driver, c.hz) - ref; got < c.lo || got > c.hi {
			t.Errorf("%s at %g Hz: %+.1f dB over 1 kHz, want %+g to %+g", v.DriverFile(), c.hz, got, c.lo, c.hi)
		}
	}
	if iirResponse(v.driver, 30) >= iirResponse(v.driver, 80) {
		t.Error("the driver EQ does not roll off below its lift")
	}
	if top := v.volEQ[len(v.volEQ)-1]; math.Abs(iirResponse(top, 100)) > 0.5 {
		t.Errorf("the loudest volume EQ is %+.1f dB at 100 Hz, not flat", iirResponse(top, 100))
	}
	if q := iirResponse(v.volEQ[0], 60) - iirResponse(v.volEQ[0], 1000); q < 6 {
		t.Errorf("the quietest volume EQ lifts 60 Hz %+.1f dB, no loudness compensation", q)
	}
}

func TestVendorCannoliLimits(t *testing.T) {
	v := vendorTuning(t)
	if v.set.Name != "cannoli" {
		t.Skipf("the %s tuning", v.set.Name)
	}
	m := v.limits.Multiband
	if m.NumBands != 4 || fmt.Sprint(m.Crossovers) != "[200 900 5000]" {
		t.Errorf("%d bands at %v", m.NumBands, m.Crossovers)
	}
	if fmt.Sprint(v.limits.USPD) != "[-2.23 -2.23 -2.23 -2.23 -2.23 -2.23 -2.23 -2.23 -1.5 -1]" ||
		fmt.Sprint(v.limits.Modes, v.limits.ModeDB) != "[5 3 1 -1] [0 -2 -3 -5]" {
		t.Errorf("throttling by volume %v, by temperature %v %v", v.limits.USPD, v.limits.Modes, v.limits.ModeDB)
	}
	for _, hz := range []float64{60, 120, 3000} {
		// At the top the full-band ceiling is the volume table's -1 dB (limits).
		if p := peakDB(run(t, v, sine(hz, -1, 2), 1)); p > -0.99 {
			t.Errorf("%g Hz near full scale peaks at %+.2f dBFS", hz, p)
		}
	}
}

// cannoliThrottling is what the Dot 5's AFE.cfg adds to cannoliAFE: both tables in the chain, and the
// tables as 8349 has them.
const cannoliThrottling = `,
    "MBL Audio Throttling":
       {
            "Speaker Power Modes" : [5, 3, 1, -1],
            "Limiter Settings"    : [0, -2, -3, -5]
       },
    "USPD FBL Throttling":
       {
          // Volume Level           [    1,     2,     3,     4,     5,     6,     7,     8,    9, 10]
            "Limiter Settings"    : [-2.23, -2.23, -2.23, -2.23, -2.23, -2.23, -2.23, -2.23, -1.5, -1]
       }
}
`

// throttledAFE is cannoliAFE with the throttling tables, and the chain naming those in chain.
func throttledAFE(chain string) string {
	afe := fmt.Sprintf(cannoliAFE, "false")
	afe = strings.Replace(afe, `"DriverFBL_LR"            : "Fullband Limiter",`,
		`"DriverFBL_LR"            : "Fullband Limiter",`+chain, 1)
	return strings.TrimSuffix(strings.TrimSpace(afe), "}") + cannoliThrottling
}

// The limiters' ceilings follow Fire OS's two tables: the full-band one by volume level always, and
// both down to the thermal mode's when the unit is hot, never above the file's own.
func TestTheCannoliLimitersThrottleAsFireOSDoes(t *testing.T) {
	dir := cannoliDir(t, []iirSection{flat}, nil)
	both := `
        "MBLAudioThrottling"      :"MBL Audio Throttling",
        "USPDFBLThrottling"      :"USPD FBL Throttling",`
	if err := os.WriteFile(filepath.Join(dir, Cannoli.Limits), []byte(throttledAFE(both)), 0o600); err != nil {
		t.Fatal(err)
	}
	tun, err := LoadSet(dir, Cannoli)
	if err != nil {
		t.Fatal(err)
	}
	c, err := tun.Chain(1024)
	if err != nil {
		t.Fatal(err)
	}
	l := c.comp.(*limited)
	ceil := func() string {
		var s []string
		for _, v := range l.ceilingsDB() {
			s = append(s, fmt.Sprintf("%.2f", v))
		}
		return strings.Join(s, " ")
	}
	for _, step := range []struct {
		volume float64
		state  int
		want   string // full band, then the four bands
	}{
		{1, 0, "-1.00 -2.00 -1.00 -1.00 -1.00"},
		{0.9, 0, "-1.50 -2.00 -1.00 -1.00 -1.00"},
		{0.5, 0, "-2.23 -2.00 -1.00 -1.00 -1.00"},
		{0.05, 0, "-2.23 -2.00 -1.00 -1.00 -1.00"},
		{1, 1, "-2.00 -2.00 -2.00 -2.00 -2.00"},
		{0.5, 1, "-2.23 -2.00 -2.00 -2.00 -2.00"},
		{1, 2, "-3.00 -3.00 -3.00 -3.00 -3.00"},
		{1, 3, "-5.00 -5.00 -5.00 -5.00 -5.00"},
		{1, 7, "-5.00 -5.00 -5.00 -5.00 -5.00"},
		{1, 0, "-1.00 -2.00 -1.00 -1.00 -1.00"},
	} {
		c.Volume(step.volume)
		c.Throttle(step.state)
		if got := ceil(); got != step.want {
			t.Errorf("at %g of full volume, cooler state %d: ceilings %s, want %s", step.volume, step.state, got, step.want)
		}
	}

	// A chain that names neither table keeps the file's ceilings whatever it is told.
	if err := os.WriteFile(filepath.Join(dir, Cannoli.Limits), []byte(throttledAFE("")), 0o600); err != nil {
		t.Fatal(err)
	}
	tun, err = LoadSet(dir, Cannoli)
	if err != nil {
		t.Fatal(err)
	}
	if c, err = tun.Chain(1024); err != nil {
		t.Fatal(err)
	}
	l = c.comp.(*limited)
	c.Volume(0.3)
	c.Throttle(3)
	if got := ceil(); got != "-0.10 -2.00 -1.00 -1.00 -1.00" {
		t.Errorf("with no throttling in the chain the ceilings went to %s", got)
	}
}

// A chain that throttles by a table the file does not have plays untuned rather than louder than it
// was set.
func TestTheCannoliSetRefusesAMissingThrottlingTable(t *testing.T) {
	dir := cannoliDir(t, []iirSection{flat}, nil)
	afe := strings.Replace(fmt.Sprintf(cannoliAFE, "false"), `"DriverFBL_LR"            : "Fullband Limiter",`,
		`"DriverFBL_LR" : "Fullband Limiter", "USPDFBLThrottling" : "USPD FBL Throttling",`, 1)
	if err := os.WriteFile(filepath.Join(dir, Cannoli.Limits), []byte(afe), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSet(dir, Cannoli); err == nil {
		t.Error("a chain naming USPD FBL Throttling loaded without the table")
	}
}

// The ultrasound gets what the volume table keeps under the full-band ceiling: 10^(-0.1/20) less
// 10^(-2.23/20) to 80 %, and less at the top.
func TestTheCannoliKeepsHeadroomForUltrasound(t *testing.T) {
	dir := cannoliDir(t, []iirSection{flat}, nil)
	both := `
        "USPDFBLThrottling"      :"USPD FBL Throttling",`
	if err := os.WriteFile(filepath.Join(dir, Cannoli.Limits), []byte(throttledAFE(both)), 0o600); err != nil {
		t.Fatal(err)
	}
	tun, err := LoadSet(dir, Cannoli)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ of, db float64 }{{0.5, -13.36}, {0.9, -16.64}, {1, -20.21}} {
		a, ok := tun.UltrasoundLevel(c.of)
		if got := 20 * math.Log10(a); !ok || math.Abs(got-c.db) > 0.05 {
			t.Errorf("at %g of full volume: %.2f dBFS (%v), want %.2f", c.of, got, ok, c.db)
		}
	}
	if _, ok := tun.Chain(1024); ok != nil {
		t.Fatal(ok)
	}
	plain := cannoliDir(t, []iirSection{flat}, nil)
	if tun, err = LoadSet(plain, Cannoli); err != nil {
		t.Fatal(err)
	}
	if _, ok := tun.UltrasoundLevel(0.5); ok {
		t.Error("a tuning without the table says it keeps headroom")
	}
}
