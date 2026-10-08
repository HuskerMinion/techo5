// Package moon works out where the moon is and how much of it is lit, from a place and a time, with no
// service to ask: the weather art draws the moon in its sky from it, at night and, paler, by day.
//
// The moon's place is the low-precision formula from The Astronomical Almanac (section D, "Low
// precision formulae for the Moon's coordinates"), good to about 0.3 degrees, and the sun's is the
// Almanac's low-precision one too (section C). Rising and setting follow Meeus, "Astronomical
// Algorithms" (2nd ed., chapter 15): the moon's center is at h0 = 0.7275 * parallax - 0m34s of
// altitude, the horizon's dip from the air's bending and the moon's nearness both counted. The times
// come out within a few minutes of the U.S. Naval Observatory's, which is closer than a picture needs.
package moon

import (
	"math"
	"time"
)

// Phase is how much of the moon is lit, and which way it is going.
type Phase struct {
	Fraction float64 // of the disc lit, 0 at new moon, 1 at full
	Waxing   bool    // growing toward full: lit on the right, seen from the northern hemisphere
	Elong    float64 // the moon's longitude past the sun's, degrees 0 to 360; 0 is new, 180 full
}

// Name is the phase as an almanac names it. The four principal phases are moments; each name here
// covers about a day around its moment, the way a calendar shows them.
func (p Phase) Name() string {
	e := p.Elong
	near := func(a float64) bool { return math.Abs(math.Remainder(e-a, 360)) < 6 }
	switch {
	case near(0):
		return "New moon"
	case near(90):
		return "First quarter"
	case near(180):
		return "Full moon"
	case near(270):
		return "Last quarter"
	case e < 90:
		return "Waxing crescent"
	case e < 180:
		return "Waxing gibbous"
	case e < 270:
		return "Waning gibbous"
	}
	return "Waning crescent"
}

// PhaseAt is the moon's phase at t, the same everywhere on Earth.
func PhaseAt(t time.Time) Phase {
	d := days(t)
	lm, bm, par := moonEcliptic(d)
	ls, rs := sunEcliptic(d)
	// The angle at the Earth between the sun and the moon, then the angle at the moon between the sun
	// and the Earth, which is what sets how much of the face we see is lit.
	cosPsi := math.Cos(rad(bm)) * math.Cos(rad(lm-ls))
	psi := math.Acos(math.Max(-1, math.Min(1, cosPsi)))
	dist := 6378.14 / math.Sin(rad(par)) // km
	sunKm := rs * 149597870.7
	i := math.Atan2(sunKm*math.Sin(psi), dist-sunKm*math.Cos(psi))
	e := math.Mod(lm-ls, 360)
	if e < 0 {
		e += 360
	}
	return Phase{Fraction: (1 + math.Cos(i)) / 2, Waxing: e < 180, Elong: e}
}

// Sky is the moon's pass across the sky at a moment: whether it is up, and when it rose and will set.
type Sky struct {
	Up        bool
	Rise, Set time.Time // while Up, this pass's; either is zero when not found within a day and a half
	HourAngle float64   // degrees west of the meridian, -180 to 180: where it is when Rise or Set is zero
}

// Along is how far through its pass the moon is, 0 at moonrise and 1 at moonset; when the pass has no
// rise or set near enough to find (the moon up for days, near the poles), it goes by the moon's hour
// angle instead, a half at the meridian.
func (s Sky) Along(now time.Time) float64 {
	if !s.Rise.IsZero() && !s.Set.IsZero() && s.Set.After(s.Rise) {
		return math.Max(0, math.Min(1, float64(now.Sub(s.Rise))/float64(s.Set.Sub(s.Rise))))
	}
	return 0.5 + s.HourAngle/360
}

// scan is the step the passes are searched in: a moon up for less than this, which happens only in a
// grazing pass near the poles, can be missed.
const scan = 10 * time.Minute

// At is the moon's pass at lat, lon at now.
func At(lat, lon float64, now time.Time) Sky {
	alt, h0 := altHorizon(lat, lon, days(now))
	var s Sky
	s.HourAngle = hourAngle(lon, days(now))
	if alt < h0 {
		return s
	}
	s.Up = true
	f := func(t time.Time) float64 { a, h := altHorizon(lat, lon, days(t)); return a - h }
	limit := 36 * time.Hour
	for t := now; now.Sub(t) < limit; t = t.Add(-scan) {
		if f(t.Add(-scan)) < 0 {
			s.Rise = cross(f, t.Add(-scan), t)
			break
		}
	}
	for t := now; t.Sub(now) < limit; t = t.Add(scan) {
		if f(t.Add(scan)) < 0 {
			s.Set = cross(f, t, t.Add(scan))
			break
		}
	}
	return s
}

// Times is when the moon rises and sets during day (midnight to midnight in day's location) at lat,
// lon. The moon comes up about fifty minutes later each day, so some days have no rise or no set, and
// near the poles a day can have neither: hasRise and hasSet say which were found.
func Times(lat, lon float64, day time.Time) (rise, set time.Time, hasRise, hasSet bool) {
	loc := day.Location()
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	end := time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, loc) // a DST day is not 24 hours
	f := func(t time.Time) float64 { a, h := altHorizon(lat, lon, days(t)); return a - h }
	prev := f(start)
	for t := start; t.Before(end); {
		next := t.Add(scan)
		if next.After(end) {
			next = end
		}
		v := f(next)
		switch {
		case prev < 0 && v >= 0 && !hasRise:
			rise, hasRise = cross(f, t, next).In(loc), true
		case prev >= 0 && v < 0 && !hasSet:
			set, hasSet = cross(f, t, next).In(loc), true
		}
		prev, t = v, next
	}
	return
}

// cross is where f changes sign between a and b, to the second.
func cross(f func(time.Time) float64, a, b time.Time) time.Time {
	fa := f(a)
	for b.Sub(a) > time.Second {
		m := a.Add(b.Sub(a) / 2)
		if fm := f(m); (fm < 0) == (fa < 0) {
			a, fa = m, fm
		} else {
			b = m
		}
	}
	return a
}

// altHorizon is the moon's geocentric altitude at d, and the altitude its center is at when its upper
// edge touches the horizon (h0, after Meeus), both in degrees.
func altHorizon(lat, lon, d float64) (alt, h0 float64) {
	l, b, par := moonEcliptic(d)
	ra, dec := equatorial(l, b, d)
	ha := rad(siderealDeg(d) + lon - ra)
	phi := rad(lat)
	alt = deg(math.Asin(math.Sin(phi)*math.Sin(rad(dec)) + math.Cos(phi)*math.Cos(rad(dec))*math.Cos(ha)))
	return alt, 0.7275*par - 34.0/60
}

// hourAngle is the moon's hour angle at d, west of the meridian at lon, in -180 to 180 degrees.
func hourAngle(lon, d float64) float64 {
	l, b, _ := moonEcliptic(d)
	ra, _ := equatorial(l, b, d)
	return math.Remainder(siderealDeg(d)+lon-ra, 360)
}

// moonEcliptic is the moon's ecliptic longitude, latitude and horizontal parallax at d, in degrees
// (the Almanac's low-precision series).
func moonEcliptic(d float64) (l, b, par float64) {
	t := d / 36525
	sin := func(a, r float64) float64 { return math.Sin(rad(a + r*t)) }
	cos := func(a, r float64) float64 { return math.Cos(rad(a + r*t)) }
	l = 218.32 + 481267.881*t +
		6.29*sin(135.0, 477198.87) - 1.27*sin(259.3, -413335.36) + 0.66*sin(235.7, 890534.22) +
		0.21*sin(269.9, 954397.74) - 0.19*sin(357.5, 35999.05) - 0.11*sin(186.5, 966404.03)
	b = 5.13*sin(93.3, 483202.02) + 0.28*sin(228.2, 960400.89) - 0.28*sin(318.3, 6003.15) - 0.17*sin(217.6, -407332.21)
	par = 0.9508 + 0.0518*cos(135.0, 477198.87) + 0.0095*cos(259.3, -413335.36) +
		0.0078*cos(235.7, 890534.22) + 0.0028*cos(269.9, 954397.74)
	return math.Mod(l, 360), b, par
}

// sunEcliptic is the sun's ecliptic longitude in degrees and its distance in astronomical units at d
// (the Almanac's low-precision sun).
func sunEcliptic(d float64) (l, r float64) {
	g := rad(357.528 + 0.9856003*d)
	l = 280.460 + 0.9856474*d + 1.915*math.Sin(g) + 0.020*math.Sin(2*g)
	return math.Mod(l, 360), 1.00014 - 0.01671*math.Cos(g) - 0.00014*math.Cos(2*g)
}

// equatorial is ecliptic longitude l and latitude b (degrees) as right ascension and declination.
func equatorial(l, b, d float64) (ra, dec float64) {
	e := rad(23.439 - 0.0000004*d)
	lr, br := rad(l), rad(b)
	ra = deg(math.Atan2(math.Sin(lr)*math.Cos(e)-math.Tan(br)*math.Sin(e), math.Cos(lr)))
	dec = deg(math.Asin(math.Sin(br)*math.Cos(e) + math.Cos(br)*math.Sin(e)*math.Sin(lr)))
	return ra, dec
}

// siderealDeg is Greenwich mean sidereal time at d, in degrees.
func siderealDeg(d float64) float64 { return 280.46061837 + 360.98564736629*d }

// days is t as days since J2000.0 (2000 January 1, 12:00 TT, taken as UTC: the minute or so between
// them moves the moon by a fiftieth of its own width).
func days(t time.Time) float64 { return float64(t.UnixNano())/86400e9 + 2440587.5 - 2451545.0 }

func rad(d float64) float64 { return d * math.Pi / 180 }
func deg(r float64) float64 { return r * 180 / math.Pi }
