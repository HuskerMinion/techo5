package asp

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The Echo Dot 5th gen's (cannoli) chain is a fourth design, and not a FIR one. Its AFE.cfg ("Hardware
// Definition": "Cannoli", 8349 system.img, read 2026-11-22) runs the playback as
//
//	"UserEQ"           : "UserEQ",                 every band at 0 dB
//	"VolumeEQ"         : "Volume Dependent EQ",    VolEQ_10.cfg .. VolEQ_100.cfg, by volume
//	"DriverIIREQ"      : "DriverIIREQ",            Singleton_DEQ.cfg, by speaker vendor
//	"MBLimiter_LR"     : "Multiband Limiter",      four bands at 200, 900 and 5000 Hz
//	"DriverFBL_LR"     : "Fullband Limiter",       -0.1 dB
//	"MBLAudioThrottling": "MBL Audio Throttling",  both limiters down when the unit runs hot
//	"USPDFBLThrottling" : "USPD FBL Throttling",   the full-band ceiling by volume
//
// with the volume in front of it all (Fire OS's curve, paths_cannoli.go). The EQ_100.cfg beside them is
// referenced by nothing, and neither is MBCL.cfg: the protection here is the two limiters, not a
// compressor.
//
// Each IIR file is a list of biquads as a1, a2, b0, b1 and b2 rows of four, the denominator 1 + a1 z^-1
// + a2 z^-2, at 48 kHz. libasp.so reads a file as one filter ("Number of Eqs is %d. It should be 1 or
// same as Number of Channels"), so Singleton_DEQ.cfg's four sets of four are sixteen sections in series:
// together they lift 60-120 Hz some 17 dB over 1 kHz, take 2.5-3.5 kHz down about 7, and roll off under
// 50 Hz. Its loudest VolEQ is flat; the quietest adds up to 12 dB of bass and 5 of air.
//
// The driver EQ is per speaker vendor ("Driver Dependent EQ", ManufacturerName GSE, GTK and GGEC, the
// one fitted named by /proc/idme/speaker_vendor), with Singleton_DEQ.cfg the default. On 8349 all four
// files hold the same filter.

// SpeakerVendor is where a unit says which speaker it was built with. A variable for the tests.
var SpeakerVendor = "/proc/idme/speaker_vendor"

// iirSection is one biquad of a vendor IIR file.
type iirSection struct{ a1, a2, b0, b1, b2 float64 }

// readIIR parses an IIR file's "Coefficients" into its sections, in the order the file lists them.
func readIIR(path string) ([]iirSection, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f struct {
		Coefficients []float64 `json:"Coefficients"`
	}
	if err := json.Unmarshal(stripComments(raw), &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c := f.Coefficients
	// Rows of four, five rows a set: a1, a2, b0, b1, b2.
	if len(c) == 0 || len(c)%20 != 0 {
		return nil, fmt.Errorf("%s: %d coefficients, not sets of four biquads", path, len(c))
	}
	var out []iirSection
	for set := 0; set < len(c); set += 20 {
		for k := range 4 {
			s := iirSection{a1: c[set+k], a2: c[set+4+k], b0: c[set+8+k], b1: c[set+12+k], b2: c[set+16+k]}
			// A section whose poles are not inside the unit circle is not a filter, it is an oscillator:
			// refused, and the tuning with it, rather than let it run away on the driver.
			if math.Abs(s.a2) >= 1 || math.Abs(s.a1) >= 1+s.a2 {
				return nil, fmt.Errorf("%s: biquad %d is unstable (a1 %g, a2 %g)", path, len(out)+1, s.a1, s.a2)
			}
			out = append(out, s)
		}
	}
	return out, nil
}

// cascade is IIR sections in series, each with its own history.
type cascade []*biquad

func newCascade(s []iirSection) cascade {
	c := make(cascade, len(s))
	for i, v := range s {
		c[i] = &biquad{b0: v.b0, b1: v.b1, b2: v.b2, a1: v.a1, a2: v.a2}
	}
	return c
}

// use takes another filter's coefficients and keeps the history: the volume EQs are gentle shelves of
// the same layout, and a filter switched at its own state moves the sound rather than clicking.
func (c cascade) use(s []iirSection) {
	for i, v := range s {
		f := c[i]
		f.b0, f.b1, f.b2, f.a1, f.a2 = v.b0, v.b1, v.b2, v.a1, v.a2
	}
}

func (c cascade) process(x []float32) {
	for _, f := range c {
		for i, v := range x {
			x[i] = f.step(v)
		}
	}
}

func (c cascade) reset() {
	for _, f := range c {
		f.z1, f.z2 = 0, 0
	}
}

// iirResponse is the cascade's gain at hz in dB, for the tests and the tools.
func iirResponse(s []iirSection, hz float64) float64 {
	w := 2 * math.Pi * hz / Rate
	z1 := complex(math.Cos(w), -math.Sin(w))
	z2 := z1 * z1
	h := complex(1, 0)
	for _, v := range s {
		h *= (complex(v.b0, 0) + complex(v.b1, 0)*z1 + complex(v.b2, 0)*z2) /
			(1 + complex(v.a1, 0)*z1 + complex(v.a2, 0)*z2)
	}
	return 20 * math.Log10(math.Hypot(real(h), imag(h)))
}

// limits are the two limiters' settings, out of AFE.cfg, and the two tables Fire OS lowers their
// ceilings by.
//
// What libasp.so does with the tables (8349, read off its ARM code 2026-11-22; it has no symbols, so
// the functions are named by their log strings):
//
//   - "USPD FBL Throttling" (AudioFrontEnd::uspdFBLThrottle) is a full-band ceiling per volume level,
//     ten of them, the level being (volume-1)/10 of 0..100: the Volume Dependent EQ's own steps. It runs
//     on every volume change once "USPDFBLThrottling" is in the playback chain. The flag it checks is
//     set when the config is read, not when presence detection starts, so a stock Dot 5 plays with its
//     ceiling at -2.23 dB up to 80 %, -1.5 at 90 % and -1 at the top whether or not USPD is running:
//     the headroom for the ultrasound it may mix in is kept either way. The ceiling is the lowest of
//     that, the file's own -0.1, and whatever the thermal table asks.
//   - "MBL Audio Throttling" maps a speaker power mode to a ceiling: modes 5, 3, 1 and -1 (no, medium,
//     high and critical throttling) at 0, -2, -3 and -5 dB. Out of no throttling, the full-band ceiling
//     goes to the lowest of the mode's and the volume's, and each band to the lowest of its own and the
//     mode's ("Default MultiBand Threshold %f is lower than Throttling Threshold %f ... Skip"). A mode
//     the table does not list counts as its first.
//
// The power mode is thermal, not the supply: the audio HAL (AudioALSAStreamManager::
// handleThermalAudioState, audio.primary.mt8519.so) takes the kernel's audio_cooler state off a uevent
// and turns 0, 1, 2 and 3 into 5, 3, 1 and -1 (Chain.Throttle).
type limits struct {
	Multiband struct {
		Bypass      bool      `json:"Bypass"`
		NumBands    int       `json:"Num Bands"`
		Crossovers  []float64 `json:"FilterBank FC"`
		InputDB     []float64 `json:"Input Volume"`
		HoldMs      []float64 `json:"Hold Time"`
		ReleaseMs   []float64 `json:"Release Time"`
		ThresholdDB []float64 `json:"Threshold"`
		LookAheadMs float64   `json:"LookAhead Time"`
	}
	Fullband struct {
		Bypass      bool    `json:"Bypass"`
		InputDB     float64 `json:"InputVolume_dB"`
		ThresholdDB float64 `json:"Threshold_dB"`
		LookAheadMs float64 `json:"LookAhead_ms"`
		HoldMs      float64 `json:"HoldTime_ms"`
		ReleaseMs   float64 `json:"ReleaseTime_ms"`
		Excursion   bool    `json:"Enable Excursion Limiter"`
	}

	// USPD is the full-band ceiling per volume level, nil where the chain does not throttle by volume.
	USPD []float64
	// Modes and ModeDB are the thermal table, a ceiling per speaker power mode; empty where the chain
	// does not throttle by temperature.
	Modes  []int
	ModeDB []float64
}

// readLimits reads the "Multiband Limiter" and "Fullband Limiter" sections out of an AFE.cfg, which is
// too large and too loosely written to want to parse whole.
func readLimits(path string) (limits, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return limits{}, err
	}
	b := stripComments(raw)
	var l limits
	for _, s := range []struct {
		name string
		into any
	}{{"Multiband Limiter", &l.Multiband}, {"Fullband Limiter", &l.Fullband}} {
		sec, err := section(b, s.name)
		if err != nil {
			return limits{}, fmt.Errorf("%s: %w", path, err)
		}
		if err := json.Unmarshal(sec, s.into); err != nil {
			return limits{}, fmt.Errorf("%s: %q: %w", path, s.name, err)
		}
	}

	m, f := l.Multiband, l.Fullband
	n := m.NumBands
	switch {
	case m.Bypass || f.Bypass:
		// Bypassed limiters under a 17 dB bass lift would put it all on the driver.
		return limits{}, fmt.Errorf("%s: a limiter asks to be bypassed", path)
	case f.Excursion:
		return limits{}, fmt.Errorf("%s: the excursion limiter is on, which we do not have", path)
	case n < 1 || len(m.Crossovers) != n-1 || len(m.ReleaseMs) != n || len(m.ThresholdDB) != n:
		return limits{}, fmt.Errorf("%s: %d bands, %d crossovers, %d releases, %d thresholds", path, n,
			len(m.Crossovers), len(m.ReleaseMs), len(m.ThresholdDB))
	case len(m.InputDB) > 0 && len(m.InputDB) != n, len(m.HoldMs) > 0 && len(m.HoldMs) != n:
		return limits{}, fmt.Errorf("%s: per-band settings for other than %d bands", path, n)
	}
	for i, fc := range m.Crossovers {
		if fc <= 0 || fc >= Rate/2 || (i > 0 && fc <= m.Crossovers[i-1]) {
			return limits{}, fmt.Errorf("%s: crossovers %v", path, m.Crossovers)
		}
	}
	for i := range n {
		if m.ThresholdDB[i] > 0 || m.ThresholdDB[i] < -20 || m.ReleaseMs[i] <= 0 {
			return limits{}, fmt.Errorf("%s: band %d at %g dB, %g ms", path, i+1, m.ThresholdDB[i], m.ReleaseMs[i])
		}
	}
	if f.ThresholdDB > 0 || f.ThresholdDB < -20 || f.ReleaseMs <= 0 {
		return limits{}, fmt.Errorf("%s: the full-band limiter at %g dB, %g ms", path, f.ThresholdDB, f.ReleaseMs)
	}
	if err := readThrottling(b, &l); err != nil {
		return limits{}, fmt.Errorf("%s: %w", path, err)
	}
	return l, nil
}

// readThrottling reads the two throttling tables where the playback chain names them. A table the
// chain names and the file does not give, or gives wrong, is refused with the tuning: these only ever
// lower a ceiling, and a tuning that meant them plays louder than it was set without them.
func readThrottling(b []byte, l *limits) error {
	chain, err := section(b, "Playback")
	if err != nil {
		return nil // no chain to say so: the limiters as they are
	}
	names := func(key string) bool {
		return regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `"\s*:`).Match(chain)
	}
	bad := func(db float64) bool { return db > 0 || db < -20 }

	if names("USPDFBLThrottling") {
		var t struct {
			Settings []float64 `json:"Limiter Settings"`
		}
		sec, err := section(b, "USPD FBL Throttling")
		if err == nil {
			err = json.Unmarshal(sec, &t)
		}
		if err != nil {
			return fmt.Errorf("the chain throttles by volume: %w", err)
		}
		if len(t.Settings) == 0 || slices.ContainsFunc(t.Settings, bad) {
			return fmt.Errorf("USPD FBL Throttling: %v", t.Settings)
		}
		l.USPD = t.Settings
	}

	if names("MBLAudioThrottling") {
		var t struct {
			Modes    []int     `json:"Speaker Power Modes"`
			Settings []float64 `json:"Limiter Settings"`
		}
		sec, err := section(b, "MBL Audio Throttling")
		if err == nil {
			err = json.Unmarshal(sec, &t)
		}
		if err != nil {
			return fmt.Errorf("the chain throttles by temperature: %w", err)
		}
		if len(t.Modes) == 0 || len(t.Modes) != len(t.Settings) || slices.ContainsFunc(t.Settings, bad) {
			return fmt.Errorf("MBL Audio Throttling: modes %v, settings %v", t.Modes, t.Settings)
		}
		l.Modes, l.ModeDB = t.Modes, t.Settings
	}
	return nil
}

// section is the object AFE.cfg gives as "name": {...}, where name is a key and not one of the values
// that name the same algorithm in the chain's list.
func section(b []byte, name string) ([]byte, error) {
	re := regexp.MustCompile(`"` + regexp.QuoteMeta(name) + `"\s*:\s*\{`)
	at := re.FindIndex(b)
	if at == nil {
		return nil, fmt.Errorf("no %q", name)
	}
	start := at[1] - 1
	depth := 0
	for i := start; i < len(b); i++ {
		switch b[i] {
		case '"':
			for i++; i < len(b) && b[i] != '"'; i++ {
				if b[i] == '\\' {
					i++
				}
			}
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				// The vendor's own parser takes a trailing comma; Go's does not.
				return []byte(trailingComma.ReplaceAllString(string(b[start:i+1]), "$1")), nil
			}
		}
	}
	return nil, fmt.Errorf("%q does not close", name)
}

var trailingComma = regexp.MustCompile(`,(\s*[}\]])`)

// limited is the multiband limiter and the full-band one after it: the cannoli chain's protection.
type limited struct {
	split  *crossover
	gain   []float32
	bands  []*limiter
	work   [][]float32
	inFull float32
	full   *limiter

	// The ceilings the file sets, and what lowers them (limits): the tables, the volume level, and the
	// speaker power mode's place in the thermal table (0 is no throttling).
	fullDB float64
	bandDB []float64
	uspd   []float64
	modes  []int
	modeDB []float64
	level  int
	mode   int
}

// audioCoolerModes is the audio HAL's speaker power mode for each audio_cooler state (limits).
var audioCoolerModes = [...]int{5, 3, 1, -1}

// volume sets the full-band ceiling for a volume level, a Volume Dependent EQ step.
func (s *limited) volume(level int) {
	if s.uspd == nil {
		return
	}
	if level = max(0, min(level, len(s.uspd)-1)); level != s.level {
		s.level = level
		s.ceilings()
	}
}

// throttle sets the speaker power mode for an audio_cooler state, 0 for none to 3 for the most. A
// state past the HAL's four is taken as the last; the kernel's cooler stops at 3 (its max_level).
func (s *limited) throttle(state int) {
	if len(s.modes) == 0 {
		return
	}
	want := audioCoolerModes[max(0, min(state, len(audioCoolerModes)-1))]
	if at := max(0, slices.Index(s.modes, want)); at != s.mode {
		s.mode = at
		s.ceilings()
	}
}

// ceilings puts the limiters where the file, the volume and the temperature together say.
func (s *limited) ceilings() {
	full := s.fullDB
	if s.uspd != nil {
		full = min(full, s.uspd[s.level])
	}
	hot := s.mode > 0
	if hot {
		full = min(full, s.modeDB[s.mode])
	}
	s.full.setCeiling(full)
	for i, l := range s.bands {
		db := s.bandDB[i]
		if hot {
			db = min(db, s.modeDB[s.mode])
		}
		l.setCeiling(db)
	}
}

// ceilingsDB are the limiters' ceilings in dB, full band first, for the tests.
func (s *limited) ceilingsDB() []float64 {
	db := func(l *limiter) float64 { return 20 * math.Log10(l.ceiling) }
	out := []float64{db(s.full)}
	for _, l := range s.bands {
		out = append(out, db(l))
	}
	return out
}

func newLimited(l limits, rate int) *limited {
	m, f := l.Multiband, l.Fullband
	s := &limited{
		split:  newCrossover(m.Crossovers, rate),
		inFull: float32(dbToLinear(f.InputDB)),
		full: newLimiterWith(f.ThresholdDB, millis(f.ReleaseMs), millis(f.HoldMs),
			aheadOr(f.LookAheadMs), rate),
		fullDB: f.ThresholdDB,
		bandDB: m.ThresholdDB,
		uspd:   l.USPD,
		modes:  l.Modes,
		modeDB: l.ModeDB,
	}
	for i := range m.NumBands {
		in, hold := 0.0, 0.0
		if len(m.InputDB) > 0 {
			in = m.InputDB[i]
		}
		if len(m.HoldMs) > 0 {
			hold = m.HoldMs[i]
		}
		s.gain = append(s.gain, float32(dbToLinear(in)))
		s.bands = append(s.bands, newLimiterWith(m.ThresholdDB[i], millis(m.ReleaseMs[i]), millis(hold),
			aheadOr(m.LookAheadMs), rate))
	}
	// Until told otherwise: the loudest volume level, where the volume EQ starts too (Chain), and cool.
	s.level = max(0, len(s.uspd)-1)
	s.ceilings()
	return s
}

// aheadOr is a lookahead in milliseconds, or the one lib/asp uses where a file gives none.
func aheadOr(ms float64) time.Duration {
	if ms <= 0 {
		return lookahead
	}
	return millis(ms)
}

func (s *limited) process(x []float32) {
	if len(s.work) != len(s.bands) || len(s.work[0]) != len(x) {
		s.work = make([][]float32, len(s.bands))
		for i := range s.work {
			s.work[i] = make([]float32, len(x))
		}
	}
	s.split.process(x, s.work)
	for i, l := range s.bands {
		if g := s.gain[i]; g != 1 {
			for j := range s.work[i] {
				s.work[i][j] *= g
			}
		}
		l.process(s.work[i])
	}
	for i := range x {
		var sum float32
		for _, band := range s.work {
			sum += band[i]
		}
		x[i] = sum * s.inFull
	}
	s.full.process(x)
}

func (s *limited) reset() {
	s.split.reset()
	for _, l := range s.bands {
		l.reset()
	}
	s.full.reset()
}

// loadCannoli reads the IIR set's files: the driver EQ, by vendor where the unit names one with a file
// of its own, the volume EQs, and the limiters.
func (t *Tuning) loadCannoli(dir string) error {
	name := t.set.IIR
	if b, err := os.ReadFile(SpeakerVendor); err == nil {
		v := strings.ToUpper(strings.TrimSpace(string(b)))
		if v != "" && !strings.ContainsAny(v, `/\.`) {
			if _, err := os.Stat(filepath.Join(dir, v+"_"+name)); err == nil {
				name = v + "_" + name
			}
		}
	}
	d, err := readIIR(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	t.driver, t.driverFile = d, name

	for _, e := range t.set.VolEQ {
		v, err := readIIR(filepath.Join(dir, e.Name))
		if err != nil {
			return err
		}
		if len(t.volEQ) > 0 && len(v) != len(t.volEQ[0]) {
			return fmt.Errorf("asp: %s has %d biquads, %s %d", e.Name, len(v), t.set.VolEQ[0].Name, len(t.volEQ[0]))
		}
		t.volEQ = append(t.volEQ, v)
	}

	l, err := readLimits(filepath.Join(dir, t.set.Limits))
	if err != nil {
		return err
	}
	// The volume table goes by the volume EQ's steps, so it has to have as many.
	if l.USPD != nil && len(l.USPD) != len(t.set.VolEQ) {
		return fmt.Errorf("asp: USPD FBL Throttling has %d levels, the volume EQ %d", len(l.USPD), len(t.set.VolEQ))
	}
	t.limits = l
	return nil
}

// volBucket is which volume EQ a fraction of full volume uses, as Bucket does for the FIR sets.
func (t *Tuning) volBucket(of float64) int {
	for i, e := range t.set.VolEQ {
		if of <= e.UpTo {
			return i
		}
	}
	return len(t.set.VolEQ) - 1
}

// DriverFile is the driver EQ file the tuning loaded, which names the speaker vendor where the unit
// had one of its own; empty for a FIR set.
func (t *Tuning) DriverFile() string { return t.driverFile }
