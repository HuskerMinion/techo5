//go:build !dot

package ambient

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The conversion follows the stock driver: IR is half of what the colors add up to beyond clear,
// taken off each color; the weighted colors are divided by the counts per lux. Made-up counts.
func TestCountsBecomeLuxAsTheStockDriverDoes(t *testing.T) {
	n := counts{c: 1000, r: 400, g: 400, b: 300}
	// IR = floor((400+400+300-1000+1)/2) = 50; R', G', B' = 350, 350, 250.
	// CPL = 278 ms * 16 / 446; lux = (0.09*350 + 350 - 0.46*250) / CPL.
	want := (0.09*350 + 350 - 0.46*250) / (278.0 * 16 / 446)
	if got := tcs3400Lux(n, 16, 0.278, false); math.Abs(got-want) > 1e-9 {
		t.Errorf("lux %v, want %v", got, want)
	}
	if got := tcs3400Lux(n, 16, 0.278, false); math.Abs(got-26.72) > 0.01 {
		t.Errorf("lux %v, want about 26.72", got)
	}

	// At the highest gain the stock driver leaves IR in.
	wantTop := (0.09*400 + 400 - 0.46*300) / (278.0 * 64 / 446)
	if got := tcs3400Lux(n, 64, 0.278, true); math.Abs(got-wantTop) > 1e-9 {
		t.Errorf("top-gain lux %v, want %v", got, wantTop)
	}

	// Four times the gain and four times the counts is the same light.
	big := counts{c: 4000, r: 1600, g: 1600, b: 1200}
	if a, b := tcs3400Lux(n, 4, 0.278, false), tcs3400Lux(big, 16, 0.278, false); math.Abs(a-b) > 0.5 {
		t.Errorf("1x the counts at 4x gain read %v, 4x the counts at 16x read %v", a, b)
	}
}

func TestLuxNeverGoesBelowZero(t *testing.T) {
	cases := map[string]counts{
		"dark":       {},
		"blue light": {c: 100, r: 2, g: 5, b: 90},
		"more clear": {c: 500, r: 50, g: 50, b: 50}, // IR would be negative: it is 0
	}
	for name, n := range cases {
		if got := tcs3400Lux(n, 64, 0.278, false); got < 0 {
			t.Errorf("%s: lux %v", name, got)
		}
	}
	if got := tcs3400Lux(counts{c: 500, r: 50, g: 50, b: 50}, 1, 0.278, false); got <= 0 {
		t.Errorf("a negative IR estimate was subtracted: lux %v", got)
	}
}

func TestMaxCount(t *testing.T) {
	if got := maxCount(0.278); got != 65535 {
		t.Errorf("100 cycles: %v, want the 16-bit limit", got)
	}
	if got := maxCount(0.0278); got != 10240 {
		t.Errorf("10 cycles: %v, want 10240", got)
	}
}

func TestGainFollowsTheRoom(t *testing.T) {
	gains := []int{1, 4, 16, 64}
	const max = 65535.0
	cases := []struct {
		name  string
		clear float64
		cur   int
		want  int
	}{
		{"saturated drops a step", 60000, 2, 1},
		{"saturated at the lowest stays", 60000, 0, 0},
		{"dim rises a step", 1000, 1, 2},
		{"dim at the highest stays", 10, 3, 3},
		{"middle stays", 20000, 2, 2},
		// 7000 * 4 = 28000 is over half of 80% of full scale (26214): rising would hunt.
		{"would land too high stays", 7000, 1, 1},
	}
	for _, c := range cases {
		if got := nextGain(c.clear, gains, c.cur, max); got != c.want {
			t.Errorf("%s: gain step %d, want %d", c.name, got, c.want)
		}
	}
}

func TestParseGains(t *testing.T) {
	got := parseGains("1 4 16 64\n")
	if len(got) != 4 || got[3] != 64 {
		t.Errorf("parsed %v", got)
	}
	if got := parseGains("\n"); len(got) != 0 {
		t.Errorf("parsed %v from nothing", got)
	}
}

// fakeIIO lays out two IIO devices the way the mainline kernel does: the auxadc and the sensor.
func fakeIIO(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"iio:device0/name":                          "mt6577-auxadc\n",
		"iio:device1/name":                          "tcs3400\n",
		"iio:device1/in_intensity_integration_time": "0.002780\n",
		"iio:device1/in_intensity_calibscale":       "1\n",
		"iio:device1/calibscale_available":          "1 4 16 64\n",
		"iio:device1/in_intensity_clear_raw":        "10000\n",
		"iio:device1/in_intensity_red_raw":          "4000\n",
		"iio:device1/in_intensity_green_raw":        "4000\n",
		"iio:device1/in_intensity_blue_raw":         "3000\n",
	}
	for name, v := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := iioRoot
	iioRoot = root
	t.Cleanup(func() { iioRoot = old })
	return root
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestTheSensorIsFoundByName(t *testing.T) {
	root := fakeIIO(t)
	dir, err := findIIO("tcs3400")
	if err != nil || dir != filepath.Join(root, "iio:device1") {
		t.Fatalf("found %q (%v)", dir, err)
	}
	if _, err := findIIO("tcs3472"); err == nil {
		t.Error("found a device that is not there")
	}
}

func TestOpeningSetsTimeAndGainThenReads(t *testing.T) {
	root := fakeIIO(t)
	dev := filepath.Join(root, "iio:device1")
	s, err := openTCS3400()
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dev, "in_intensity_integration_time")); got != tcs3400IntTime {
		t.Errorf("integration time %q, want %q", got, tcs3400IntTime)
	}
	if got := read(t, filepath.Join(dev, "in_intensity_calibscale")); got != "16" {
		t.Errorf("starting gain %q, want 16", got)
	}

	// The first poll after a gain change is dropped.
	if _, ok, err := s.sample(); ok || err != nil {
		t.Fatalf("first reading after setting the gain: ok %v, err %v", ok, err)
	}
	lux, ok, err := s.sample()
	if !ok || err != nil {
		t.Fatalf("second reading: ok %v, err %v", ok, err)
	}
	// Ten times the counts of the first test: bright enough to stay at 16x.
	if math.Abs(lux-267.2) > 0.1 {
		t.Errorf("lux %v, want about 267.2", lux)
	}

	// A bright room drops the gain, and that reading is not reported.
	if err := os.WriteFile(filepath.Join(dev, "in_intensity_clear_raw"), []byte("65535\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.sample(); ok {
		t.Error("a saturated reading was reported")
	}
	if got := read(t, filepath.Join(dev, "in_intensity_calibscale")); got != "4" {
		t.Errorf("gain after saturation %q, want 4", got)
	}
}

func TestOpeningFailsWithoutTheSensor(t *testing.T) {
	root := fakeIIO(t)
	if err := os.RemoveAll(filepath.Join(root, "iio:device1")); err != nil {
		t.Fatal(err)
	}
	if _, err := openTCS3400(); err == nil {
		t.Fatal("opened a sensor that is not there")
	}
}

// Every reading keeps Current fresh; listeners hear only a change.
func TestListenersHearOnlyChanges(t *testing.T) {
	s := &Sensor{}
	var heard []float64
	s.Lux.Listen(func(v float64) { heard = append(heard, v) })
	for _, v := range []float64{12, 12, 12, 30, 30} {
		s.record(v)
	}
	if len(heard) != 2 || heard[0] != 12 || heard[1] != 30 {
		t.Errorf("heard %v, want [12 30]", heard)
	}
	if lux, _, ok := s.Current(); !ok || lux != 30 {
		t.Errorf("current %v (%v), want 30", lux, ok)
	}

	// The first reading is news even when it is zero, a dark room.
	d := &Sensor{}
	n := 0
	d.Lux.Listen(func(float64) { n++ })
	d.record(0)
	if n != 1 {
		t.Errorf("a first reading of 0 was not heard")
	}
}

// Close before Run, or during it, leaves Run nothing to read rather than a nil sensor to read from.
func TestCloseBeforeOrDuringRun(t *testing.T) {
	fakeIIO(t)
	open := func() *Sensor {
		tcs, err := openTCS3400()
		if err != nil {
			t.Fatal(err)
		}
		return &Sensor{poll: tcs}
	}

	s := open()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Run(context.Background()); !errors.Is(err, errNotOpen) {
		t.Errorf("Run after Close: %v, want %v", err, errNotOpen)
	}

	s = open()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	cancel()
	// Either Run took the sensor before Close and polled it until canceled, or Close came first.
	if err := <-done; err != nil && !errors.Is(err, errNotOpen) {
		t.Errorf("Run with Close under it: %v", err)
	}
}
