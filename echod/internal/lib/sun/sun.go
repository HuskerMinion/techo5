// Package sun works out when the sun rises and sets, from a place and a day, with no service to ask:
// the Sun clock style draws the day's light from it, on a device with or without Home Assistant.
//
// It is the NOAA sunrise equation in its short form (the one the U.S. Naval Observatory's almanac
// agrees with to about a minute outside the polar circles), which is closer than a clock face needs.
package sun

import (
	"math"
	"time"
)

// Times is when the sun rises and sets on day at lat, lon (degrees, north and east positive), in day's
// location. ok is false where the sun does not rise or does not set that day, near the poles.
func Times(lat, lon float64, day time.Time) (rise, set time.Time, ok bool) {
	loc := day.Location()
	noonUTC := time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, time.UTC)
	// Days since J2000.0, at local noon: the sun's place moves little in a day, so noon stands for it.
	n := math.Round(float64(noonUTC.Unix())/86400+2440587.5-2451545.0+0.0008) - lon/360

	m := math.Mod(357.5291+0.98560028*n, 360) // mean anomaly
	mr := rad(m)
	c := 1.9148*math.Sin(mr) + 0.0200*math.Sin(2*mr) + 0.0003*math.Sin(3*mr) // equation of the center
	l := math.Mod(m+c+180+102.9372, 360)                                     // ecliptic longitude
	transit := 2451545.0 + n + 0.0053*math.Sin(mr) - 0.0069*math.Sin(2*rad(l))
	decl := math.Asin(math.Sin(rad(l)) * math.Sin(rad(23.4397)))

	// The sun's upper edge on the horizon, with the air's bending: 0.833 degrees below it.
	cosH := (math.Sin(rad(-0.833)) - math.Sin(rad(lat))*math.Sin(decl)) / (math.Cos(rad(lat)) * math.Cos(decl))
	if cosH < -1 || cosH > 1 {
		return time.Time{}, time.Time{}, false
	}
	h := deg(math.Acos(cosH)) / 360
	return julian(transit - h).In(loc), julian(transit + h).In(loc), true
}

// Up is whether the sun's upper edge is above the horizon at lat, lon at t: what decides day and night
// where Times has no answer, the polar night and the midnight sun.
func Up(lat, lon float64, t time.Time) bool { return Altitude(lat, lon, t) > -0.833 }

// Altitude is the sun's center above the horizon at lat, lon at t, in degrees (negative below), from
// the same short solar formulas as Times: good to a fraction of a degree.
func Altitude(lat, lon float64, t time.Time) float64 {
	d := float64(t.Unix())/86400 + 2440587.5 - 2451545.0 // days since J2000.0
	m := rad(math.Mod(357.5291+0.98560028*d, 360))
	c := 1.9148*math.Sin(m) + 0.0200*math.Sin(2*m) + 0.0003*math.Sin(3*m)
	l := rad(math.Mod(deg(m)+c+180+102.9372, 360)) // ecliptic longitude
	eps := rad(23.4397)
	decl := math.Asin(math.Sin(l) * math.Sin(eps))
	ra := math.Atan2(math.Cos(eps)*math.Sin(l), math.Cos(l))
	gmst := rad(math.Mod(280.46061837+360.98564736629*d, 360))
	h := gmst + rad(lon) - ra // the local hour angle
	return deg(math.Asin(math.Sin(rad(lat))*math.Sin(decl) + math.Cos(rad(lat))*math.Cos(decl)*math.Cos(h)))
}

func rad(d float64) float64 { return d * math.Pi / 180 }
func deg(r float64) float64 { return r * 180 / math.Pi }

// julian is a Julian date as a time.
func julian(jd float64) time.Time {
	secs := (jd - 2440587.5) * 86400
	return time.Unix(0, int64(secs*1e9)).UTC()
}
