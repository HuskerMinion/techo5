package moon

import (
	"math"
	"testing"
	"time"
)

// The 2026 principal phases, in UTC, from the U.S. Naval Observatory's "Phases of the Moon" data
// (aa.usno.navy.mil/api/moon/phases/year?year=2026). A sample through the year: at each, the lit
// fraction is what the phase is, and the moment the moon passes it is within an hour of the USNO's.
var usnoPhases = []struct {
	when  string
	elong float64 // 0 new, 90 first quarter, 180 full, 270 last quarter
}{
	{"2026-01-03 10:03", 180}, {"2026-01-10 15:48", 270}, {"2026-01-18 19:52", 0}, {"2026-01-26 04:47", 90},
	{"2026-02-17 12:01", 0}, {"2026-03-03 11:38", 180}, {"2026-04-24 02:32", 90}, {"2026-05-31 08:45", 180},
	{"2026-06-08 10:00", 270}, {"2026-07-14 09:43", 0}, {"2026-08-12 17:37", 0}, {"2026-08-28 04:18", 180},
	{"2026-09-18 20:44", 90}, {"2026-10-03 13:25", 270}, {"2026-10-10 15:50", 0}, {"2026-10-26 04:12", 180},
	{"2026-12-24 01:28", 180}, {"2026-12-30 18:59", 270},
}

func TestPhases(t *testing.T) {
	for _, c := range usnoPhases {
		at, err := time.Parse("2006-01-02 15:04", c.when)
		if err != nil {
			t.Fatal(err)
		}
		p := PhaseAt(at)
		want := (1 - math.Cos(c.elong*math.Pi/180)) / 2
		if math.Abs(p.Fraction-want) > 0.02 {
			t.Errorf("%s: lit %.3f, want %.3f", c.when, p.Fraction, want)
		}
		// The moon gains about half a degree an hour on the sun.
		if off := math.Remainder(p.Elong-c.elong, 360) / 0.508; math.Abs(off) > 1 {
			t.Errorf("%s: %.0f° past the sun, %.1f hours off the USNO's moment", c.when, p.Elong, off)
		}
		names := map[float64]string{0: "New moon", 90: "First quarter", 180: "Full moon", 270: "Last quarter"}
		if n := p.Name(); n != names[c.elong] {
			t.Errorf("%s: named %q, want %q", c.when, n, names[c.elong])
		}
		if c.elong == 90 && !p.Waxing || c.elong == 270 && p.Waxing {
			t.Errorf("%s: waxing is %v", c.when, p.Waxing)
		}
	}
}

// Between the principal phases, the names in between, and the lit fraction the USNO gives for the day
// (its "fracillum", from the same rise/set pages as TestTimes). Which hour of the day that figure is
// for is not stated, and the moon's changes a few percent in a day, hence the loose check.
func TestPhaseBetween(t *testing.T) {
	for _, c := range []struct {
		at   time.Time
		name string
		lit  float64
	}{
		{time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC), "Waning crescent", 0.18},
		{time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), "Waxing gibbous", 0.69},
		{time.Date(2026, 6, 21, 2, 0, 0, 0, time.UTC), "Waxing crescent", 0.41},
		{time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC), "Waning gibbous", -1},
	} {
		p := PhaseAt(c.at)
		if p.Name() != c.name {
			t.Errorf("%s: named %q, want %q", c.at, p.Name(), c.name)
		}
		if c.lit >= 0 && math.Abs(p.Fraction-c.lit) > 0.06 {
			t.Errorf("%s: lit %.2f, want about %.2f", c.at, p.Fraction, c.lit)
		}
	}
}

// Moonrise and moonset against the USNO's "Rise/Set/Transit Times" for the day
// (aa.usno.navy.mil/api/rstt/oneday, standard or summer time as the zone says), to within three
// minutes; "" is a day the USNO lists no rise or no set.
func TestTimes(t *testing.T) {
	for _, c := range []struct {
		name      string
		lat, lon  float64
		zone      int // hours east of UTC
		day       string
		rise, set string
	}{
		{"New York", 40.7128, -74.0060, -4, "2026-10-06", "02:23", "16:34"},
		{"New York", 40.7128, -74.0060, -4, "2026-10-20", "15:28", "01:03"},
		{"London, full moon", 51.5074, -0.1278, 0, "2026-01-03", "15:48", "08:43"},
		{"London, no set", 51.5074, -0.1278, 0, "2026-01-25", "10:02", ""},
		{"Sydney", -33.8688, 151.2093, 10, "2026-06-21", "11:25", "23:39"},
		{"Sydney, full moon", -33.8688, 151.2093, 10, "2026-06-30", "16:56", "07:15"},
		{"Denver, full moon", 39.7392, -104.9903, -7, "2026-03-03", "18:27", "06:34"},
		{"Tromsø, no set", 69.6492, 18.9553, 1, "2026-12-17", "11:28", ""},
	} {
		loc := time.FixedZone("", c.zone*3600)
		day, _ := time.ParseInLocation("2006-01-02", c.day, loc)
		rise, set, hasRise, hasSet := Times(c.lat, c.lon, day)
		for _, p := range []struct {
			what string
			got  time.Time
			has  bool
			want string
		}{{"rise", rise, hasRise, c.rise}, {"set", set, hasSet, c.set}} {
			if p.want == "" {
				if p.has {
					t.Errorf("%s %s: a %s at %s, where the USNO has none", c.name, c.day, p.what, p.got.Format("15:04"))
				}
				continue
			}
			w, _ := time.ParseInLocation("2006-01-02 15:04", c.day+" "+p.want, loc)
			if !p.has {
				t.Errorf("%s %s: no %s, want %s", c.name, c.day, p.what, p.want)
			} else if d := p.got.Sub(w); d.Abs() > 3*time.Minute {
				t.Errorf("%s %s: %s %s, want %s", c.name, c.day, p.what, p.got.Format("15:04"), p.want)
			}
		}
	}
}

// Above the Arctic Circle in December the full moon does not set, and the new moon does not rise
// (the USNO: "continuously above the horizon" at Tromsø on 2026-12-24, "below" on 2026-12-10).
func TestPolar(t *testing.T) {
	loc := time.FixedZone("", 3600)
	if _, _, r, s := Times(69.6492, 18.9553, time.Date(2026, 12, 24, 0, 0, 0, 0, loc)); r || s {
		t.Errorf("Tromsø at the December full moon: rise %v, set %v", r, s)
	}
	if _, _, r, s := Times(69.6492, 18.9553, time.Date(2026, 12, 10, 0, 0, 0, 0, loc)); r || s {
		t.Errorf("Tromsø at the December new moon: rise %v, set %v", r, s)
	}
	now := time.Date(2026, 12, 24, 3, 0, 0, 0, loc)
	s := At(69.6492, 18.9553, now)
	if !s.Up {
		t.Fatal("the full moon is not up over Tromsø")
	}
	if a := s.Along(now); a < 0 || a > 1 {
		t.Errorf("along its pass: %.2f", a)
	}
	if At(69.6492, 18.9553, time.Date(2026, 12, 10, 12, 0, 0, 0, loc)).Up {
		t.Error("the new moon is up over Tromsø")
	}
}

// A pass found from inside it has the day's rise and set at its ends, and runs from 0 to 1.
func TestAt(t *testing.T) {
	loc := time.FixedZone("", -4*3600)
	day := time.Date(2026, 10, 6, 0, 0, 0, 0, loc)
	rise, set, _, _ := Times(40.7128, -74.0060, day)
	if s := At(40.7128, -74.0060, rise.Add(-20*time.Minute)); s.Up {
		t.Error("up before moonrise")
	}
	mid := rise.Add(set.Sub(rise) / 2)
	s := At(40.7128, -74.0060, mid)
	if !s.Up || s.Rise.Sub(rise).Abs() > time.Minute || s.Set.Sub(set).Abs() > time.Minute {
		t.Fatalf("pass at %s: %+v, want %s to %s", mid, s, rise, set)
	}
	if a := s.Along(mid); math.Abs(a-0.5) > 0.01 {
		t.Errorf("halfway along: %.3f", a)
	}
	if a := s.Along(rise.Add(time.Hour)); a <= 0 || a >= 0.2 {
		t.Errorf("an hour after rising: %.3f", a)
	}
}
