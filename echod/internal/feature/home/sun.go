package home

import (
	"log/slog"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/lib/sun"
)

// sunKept is today's sunrise and sunset at home. Finding home can ask Home Assistant, which a frame
// being drawn must never wait on, so home's place is looked up off the caller's goroutine, once a day;
// the times themselves are worked out from the last place found, which is quick. A new day with the
// lookup failing still gets that day's times, from where home was yesterday.
var sunKept sunKeeper

// sunKeeper is what sunKept holds.
type sunKeeper struct {
	mu        sync.Mutex
	day       string // the day rise and set are for
	rise, set time.Time
	ok        bool

	lat, lon float64
	placed   bool   // lat, lon are known
	looked   string // the day home's place was last looked up
	asking   bool
	retry    time.Time // after a failed lookup, when to try again
}

// SunTimes is today's sunrise and sunset at home, for the Sun clock style and the weather art; ok is
// false until home's place is known, and on a day the sun does not rise or set there.
func (f *Feature) SunTimes(now time.Time) (rise, set time.Time, ok bool) {
	k := &sunKept
	day := now.Format("2006-01-02")
	k.mu.Lock()
	defer k.mu.Unlock()
	k.lookUp(now, day)
	if !k.placed {
		return time.Time{}, time.Time{}, false
	}
	if k.day != day {
		k.day = day
		k.rise, k.set, k.ok = sun.Times(k.lat, k.lon, now)
	}
	return k.rise, k.set, k.ok
}

// SkyAt is a condition as the sky looks at home at now: its night form (atNight) while the sun is
// down. Night is from home's own sunrise and sunset, so it needs no Home Assistant; on a day the sun
// does not rise or set (inside the polar circles), from the sun's height at now: the polar night is
// night all day, the midnight sun day. Until home's place is known the condition is left as reported.
func (f *Feature) SkyAt(cond string, now time.Time) string {
	rise, set, ok := f.SunTimes(now)
	if !ok {
		lat, lon, placed := f.Place(now)
		if !placed || sun.Up(lat, lon, now) {
			return cond
		}
		return atNight(cond)
	}
	if !now.Before(rise) && now.Before(set) {
		return cond
	}
	return atNight(cond)
}

// SkyNow is the condition a screen shows for now: the reading, or without one today's forecast in its
// place. The reading has its night form already (Weather); the forecast's day is a daytime forecast,
// so standing in for the reading after dark it takes the night form here, or the page showed a sun
// at night. The forecast's own row keeps its day icons.
func (f *Feature) SkyNow(w Weather, days []hass.Day, now time.Time) string {
	if w.Condition != "" || len(days) == 0 {
		return w.Condition
	}
	return f.SkyAt(days[0].Condition, now)
}

// Place is home's latitude and longitude as last found, for the weather art's moon, which is worked
// out from it a minute at a time; ok is false until it is known. It looks home up as SunTimes does.
func (f *Feature) Place(now time.Time) (lat, lon float64, ok bool) {
	k := &sunKept
	k.mu.Lock()
	defer k.mu.Unlock()
	k.lookUp(now, now.Format("2006-01-02"))
	return k.lat, k.lon, k.placed
}

// lookUp starts finding home's place, once a day, unless that is under way or waiting to try again.
// The caller holds mu.
func (k *sunKeeper) lookUp(now time.Time, day string) {
	if k.looked != day && !k.asking && now.After(k.retry) {
		k.asking = true
		safe.Go("sun times", func() { sunLookUp(day) })
	}
}

// sunLookUp finds home's place, and has the times worked out again from it.
func sunLookUp(day string) {
	k := &sunKept
	defer func() { // a lookup that panicked is tried again later, rather than never
		k.mu.Lock()
		k.asking = false
		k.mu.Unlock()
	}()
	lat, lon, err := homeLocation()
	k.mu.Lock()
	defer k.mu.Unlock()
	if err != nil {
		slog.Debug("sun times wait for home's place", "err", err)
		k.retry = time.Now().Add(10 * time.Minute)
		return
	}
	k.looked = day
	if !k.placed || lat != k.lat || lon != k.lon {
		k.lat, k.lon, k.placed = lat, lon, true
		k.day = "" // worked out again from the new place on the next ask
	}
}
